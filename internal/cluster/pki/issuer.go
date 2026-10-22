package pki

import (
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"sync"
	"sync/atomic"
	"time"

	"github.com/eukalpia/unio/internal/ids"
)

// Certificate lifetimes and timings.
const (
	// DefaultLifetime is the validity of a node certificate when no lifetime is
	// given.
	DefaultLifetime = 7 * 24 * time.Hour
	// MinLifetime is the shortest lifetime an Issuer accepts. Shorter
	// certificates would renew more often than nodes can reliably run.
	MinLifetime = time.Hour

	// clockSkew backdates NotBefore so that peers whose clocks run slightly
	// behind still accept a freshly issued certificate.
	clockSkew = 5 * time.Minute

	// retryDelay is how long a failed renewal waits before it is attempted again.
	retryDelay = time.Minute
)

// IssuerOptions configures an Issuer.
type IssuerOptions struct {
	// Lifetime is the validity of each certificate, counted from issuance. Zero
	// means DefaultLifetime. Lifetimes below MinLifetime are refused.
	Lifetime time.Duration
	// Clock is the time source. Nil means SystemClock.
	Clock Clock
}

// Issuer keeps one node's certificate current against the cluster CA. It
// issues a certificate when created and issues a new one, with a new key, when
// half of the current certificate's lifetime has elapsed. The certificate
// lifetime runs from NotBefore, which is backdated by clock skew, so the
// renewal point is NotBefore plus half of NotAfter minus NotBefore.
//
// Handshakes read the current certificate with one atomic load and never wait
// for a renewal. A renewal that is under way does not block them: they present
// the certificate that is still valid. Renewals are serialized. An Issuer must
// be closed when its node stops, which stops the renewal timer.
type Issuer struct {
	ca       *CA
	node     ids.NodeID
	hosts    []string
	lifetime time.Duration
	clock    Clock
	roots    *x509.CertPool

	// state is the certificate in use. Readers load it without locking.
	state atomic.Pointer[issued]

	// mu serializes renewals and guards the timer fields below.
	mu     sync.Mutex
	timer  Timer
	gen    uint64
	closed bool

	// renewHook is a test seam. When set, a renewal calls it after the new
	// certificate is issued and before it is published, so a test can hold a
	// renewal open. Production code leaves it nil.
	renewHook func()
}

// issued is one certificate together with its renewal point.
type issued struct {
	cert    *tls.Certificate
	renewAt time.Time
}

// NewIssuer issues the first certificate for node id and starts its renewal.
// The node's hosts become DNS or IP SANs, as for IssueNode.
func (ca *CA) NewIssuer(id ids.NodeID, hosts []string, opts IssuerOptions) (*Issuer, error) {
	lifetime := opts.Lifetime
	if lifetime == 0 {
		lifetime = DefaultLifetime
	}
	if lifetime < MinLifetime {
		return nil, fmt.Errorf("pki: certificate lifetime %v is below the minimum %v", lifetime, MinLifetime)
	}
	clock := opts.Clock
	if clock == nil {
		clock = SystemClock()
	}
	is := &Issuer{
		ca:       ca,
		node:     id,
		hosts:    append([]string(nil), hosts...),
		lifetime: lifetime,
		clock:    clock,
		roots:    ca.CertPool(),
	}
	first, err := is.issue(clock.Now())
	if err != nil {
		return nil, err
	}
	is.state.Store(first)
	is.mu.Lock()
	is.armLocked(first.renewAt)
	is.mu.Unlock()
	return is, nil
}

// Identity returns the TLS identity this issuer backs. Its certificate renews
// as the issuer does.
func (is *Issuer) Identity() *Identity {
	return &Identity{Roots: is.roots, issuer: is, clock: is.clock}
}

// Certificate returns the certificate to present now. When the renewal point
// has passed, it renews the certificate, unless a renewal is already in
// progress, in which case it returns the current certificate, which is still
// valid until its NotAfter.
func (is *Issuer) Certificate() *tls.Certificate {
	st := is.state.Load()
	if is.clock.Now().Before(st.renewAt) {
		return st.cert
	}
	return is.refresh(st)
}

// GetCertificate can be used as tls.Config.GetCertificate on a server.
func (is *Issuer) GetCertificate(*tls.ClientHelloInfo) (*tls.Certificate, error) {
	return is.Certificate(), nil
}

// GetClientCertificate can be used as tls.Config.GetClientCertificate on a
// client.
func (is *Issuer) GetClientCertificate(*tls.CertificateRequestInfo) (*tls.Certificate, error) {
	return is.Certificate(), nil
}

// Close stops renewal. The last certificate stays available until it expires,
// and a closed issuer never issues another. Close is idempotent.
func (is *Issuer) Close() error {
	is.mu.Lock()
	defer is.mu.Unlock()
	is.closed = true
	if is.timer != nil {
		is.timer.Stop()
		is.timer = nil
	}
	return nil
}

// refresh renews the certificate when it is due. stale is the certificate the
// caller saw on its fast path. If another goroutine holds the renewal lock, the
// caller keeps stale, which is still valid.
func (is *Issuer) refresh(stale *issued) *tls.Certificate {
	if !is.mu.TryLock() {
		return stale.cert
	}
	defer is.mu.Unlock()
	cur := is.state.Load()
	if cur != stale || is.closed || is.clock.Now().Before(cur.renewAt) {
		return cur.cert
	}
	return is.renewLocked(cur).cert
}

// onTimer runs the renewal scheduled under generation gen. A timer that was
// replaced, or whose issuer was closed, does nothing.
func (is *Issuer) onTimer(gen uint64) {
	is.mu.Lock()
	defer is.mu.Unlock()
	if is.closed || gen != is.gen {
		return
	}
	cur := is.state.Load()
	if is.clock.Now().Before(cur.renewAt) {
		is.armLocked(cur.renewAt)
		return
	}
	is.renewLocked(cur)
}

// renewLocked issues a new certificate and publishes it. On failure the
// current certificate stays in use and renewal is retried later. The caller
// holds mu.
func (is *Issuer) renewLocked(cur *issued) *issued {
	now := is.clock.Now()
	next, err := is.issue(now)
	if err != nil {
		is.armLocked(now.Add(retryDelay))
		return cur
	}
	if is.renewHook != nil {
		is.renewHook()
	}
	is.state.Store(next)
	is.armLocked(next.renewAt)
	return next
}

// armLocked schedules the next renewal check at the given time, replacing any
// earlier schedule. The caller holds mu.
func (is *Issuer) armLocked(at time.Time) {
	if is.timer != nil {
		is.timer.Stop()
		is.timer = nil
	}
	if is.closed {
		return
	}
	is.gen++
	gen := is.gen
	is.timer = is.clock.AfterFunc(at.Sub(is.clock.Now()), func() { is.onTimer(gen) })
}

// issue creates a certificate valid from now-clockSkew for the issuer's
// lifetime, with a fresh key, and computes its renewal point.
func (is *Issuer) issue(now time.Time) (*issued, error) {
	cert, err := is.ca.issueAt(is.node, is.hosts, now.Add(-clockSkew), now.Add(is.lifetime))
	if err != nil {
		return nil, fmt.Errorf("pki: issue certificate for %s: %w", is.node, err)
	}
	nb, na := cert.Leaf.NotBefore, cert.Leaf.NotAfter
	return &issued{cert: &cert, renewAt: nb.Add(na.Sub(nb) / 2)}, nil
}
