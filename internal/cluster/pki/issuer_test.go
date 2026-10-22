package pki

import (
	"bytes"
	"crypto/tls"
	"sync"
	"testing"
	"time"
)

// silentClock keeps its time but never fires timers. It models a process that
// was paused while its renewal timer was due.
type silentClock struct{ *ManualClock }

// AfterFunc implements Clock by returning a timer that never fires.
func (silentClock) AfterFunc(time.Duration, func()) Timer { return noopTimer{} }

// noopTimer is a Timer that never fires.
type noopTimer struct{}

// Stop implements Timer.
func (noopTimer) Stop() bool { return false }

// renewalPoint is the instant at which a certificate's renewal is due.
func renewalPoint(c *tls.Certificate) time.Time {
	nb, na := c.Leaf.NotBefore, c.Leaf.NotAfter
	return nb.Add(na.Sub(nb) / 2)
}

func TestIssuerValidityDefaults(t *testing.T) {
	ca := mustCA(t, testSecret)
	clk := NewManualClock(t0)
	is := mustIssuer(t, ca, "node-a", IssuerOptions{Clock: clk})
	leaf := is.Certificate().Leaf
	if want := t0.Add(-clockSkew); !leaf.NotBefore.Equal(want) {
		t.Fatalf("NotBefore = %v, want %v (five minutes of skew)", leaf.NotBefore, want)
	}
	if want := t0.Add(DefaultLifetime); !leaf.NotAfter.Equal(want) {
		t.Fatalf("NotAfter = %v, want %v", leaf.NotAfter, want)
	}
	if DefaultLifetime != 7*24*time.Hour {
		t.Fatalf("DefaultLifetime = %v, want seven days", DefaultLifetime)
	}
	if _, err := ca.NewIssuer("node-a", nil, IssuerOptions{Lifetime: MinLifetime - time.Second, Clock: clk}); err == nil {
		t.Fatal("lifetime below one hour accepted")
	}
	short := mustIssuer(t, ca, "node-a", IssuerOptions{Lifetime: MinLifetime, Clock: clk})
	if got := short.Certificate().Leaf.NotAfter.Sub(t0); got != MinLifetime {
		t.Fatalf("minimum-lifetime certificate ends %v after issue, want %v", got, MinLifetime)
	}
}

func TestRenewalAtHalfLife(t *testing.T) {
	ca := mustCA(t, testSecret)
	clk := NewManualClock(t0)
	is := mustIssuer(t, ca, "node-a", IssuerOptions{Clock: clk})
	first := is.Certificate()
	renewAt := renewalPoint(first)

	clk.Advance(renewAt.Sub(clk.Now()) - time.Second)
	if is.Certificate() != first {
		t.Fatal("certificate renewed before half its lifetime")
	}
	clk.Advance(2 * time.Second)
	second := is.Certificate()
	if second == first {
		t.Fatal("certificate not renewed at half its lifetime")
	}
	if second.Leaf.SerialNumber.Cmp(first.Leaf.SerialNumber) == 0 {
		t.Fatal("renewal reused the serial number")
	}
	if bytes.Equal(second.Leaf.RawSubjectPublicKeyInfo, first.Leaf.RawSubjectPublicKeyInfo) {
		t.Fatal("renewal reused the node key")
	}
	if want := renewAt.Add(-clockSkew); !second.Leaf.NotBefore.Equal(want) {
		t.Fatalf("renewed NotBefore = %v, want %v", second.Leaf.NotBefore, want)
	}
	if want := renewAt.Add(DefaultLifetime); !second.Leaf.NotAfter.Equal(want) {
		t.Fatalf("renewed NotAfter = %v, want %v", second.Leaf.NotAfter, want)
	}
}

func TestCertificateStaysValidAcrossRenewals(t *testing.T) {
	ca := mustCA(t, testSecret)
	clk := NewManualClock(t0)
	is := mustIssuer(t, ca, "node-a", IssuerOptions{Clock: clk})
	serials := map[string]bool{}
	for range 30 * 24 {
		clk.Advance(time.Hour)
		c := is.Certificate()
		now := clk.Now()
		if now.Before(c.Leaf.NotBefore) || now.After(c.Leaf.NotAfter) {
			t.Fatalf("at %v the served certificate is valid only %v..%v", now, c.Leaf.NotBefore, c.Leaf.NotAfter)
		}
		serials[c.Leaf.SerialNumber.String()] = true
	}
	// A renewal every 3.5 days over 30 days is about eight certificates.
	if len(serials) < 8 {
		t.Fatalf("only %d distinct certificates over 30 days", len(serials))
	}
}

func TestMissedTimerIsRepairedOnHandshake(t *testing.T) {
	ca := mustCA(t, testSecret)
	clk := NewManualClock(t0)
	is := mustIssuer(t, ca, "node-a", IssuerOptions{Clock: silentClock{clk}})
	first := is.Certificate()
	clk.Advance(renewalPoint(first).Sub(clk.Now()) + time.Hour)
	if second := is.Certificate(); second == first {
		t.Fatal("overdue certificate not renewed on the handshake path")
	}
}

func TestCloseStopsRenewal(t *testing.T) {
	ca := mustCA(t, testSecret)
	clk := NewManualClock(t0)
	is := mustIssuer(t, ca, "node-a", IssuerOptions{Clock: clk})
	first := is.Certificate()
	if err := is.Close(); err != nil {
		t.Fatal(err)
	}
	if err := is.Close(); err != nil {
		t.Fatalf("second Close: %v", err)
	}
	clk.Advance(DefaultLifetime)
	if is.Certificate() != first {
		t.Fatal("closed issuer renewed its certificate")
	}
}

func TestExpiredCertificateRejectedByPeers(t *testing.T) {
	ca := mustCA(t, testSecret)
	clk := NewManualClock(t0)
	issA := mustIssuer(t, ca, "node-a", IssuerOptions{Clock: clk})
	idB := mustIssuer(t, ca, "node-b", IssuerOptions{Clock: clk}).Identity()
	serverCfg, err := ServerConfig(issA.Identity())
	if err != nil {
		t.Fatal(err)
	}
	clientCfg, err := ClientConfig(idB)
	if err != nil {
		t.Fatal(err)
	}
	if res := exchange(t, serverCfg, clientCfg, "node-a"); res.serverErr != nil || res.clientErr != nil {
		t.Fatalf("valid peers failed: server=%v client=%v", res.serverErr, res.clientErr)
	}

	// A stops renewing and its certificate runs out. B keeps renewing, so the
	// failure is A's alone.
	if err := issA.Close(); err != nil {
		t.Fatal(err)
	}
	clk.Advance(DefaultLifetime + time.Hour)
	res := exchange(t, serverCfg, clientCfg, "node-a")
	if res.serverErr == nil && res.clientErr == nil {
		t.Fatal("expired certificate accepted by a peer")
	}
}

func TestHandshakeDuringRenewalSucceeds(t *testing.T) {
	ca := mustCA(t, testSecret)
	clk := NewManualClock(t0)
	issA := mustIssuer(t, ca, "node-a", IssuerOptions{Clock: clk})
	idB := mustIssuer(t, ca, "node-b", IssuerOptions{Clock: clk}).Identity()
	serverCfg, err := ServerConfig(issA.Identity())
	if err != nil {
		t.Fatal(err)
	}
	clientCfg, err := ClientConfig(idB)
	if err != nil {
		t.Fatal(err)
	}

	first := issA.Certificate()
	// Hold the renewal after the new certificate is issued but before it is
	// published.
	entered := make(chan struct{})
	release := make(chan struct{})
	var once sync.Once
	issA.renewHook = func() {
		once.Do(func() {
			close(entered)
			<-release
		})
	}
	advanced := make(chan struct{})
	go func() {
		clk.Advance(renewalPoint(first).Sub(clk.Now()) + time.Hour)
		close(advanced)
	}()
	<-entered

	during := exchange(t, serverCfg, clientCfg, "node-a")
	close(release)
	<-advanced
	if during.serverErr != nil || during.clientErr != nil {
		t.Fatalf("handshake during renewal failed: server=%v client=%v", during.serverErr, during.clientErr)
	}
	if during.clientSawServer.Cmp(first.Leaf.SerialNumber) != 0 {
		t.Fatal("handshake during renewal did not present the still-valid certificate")
	}

	after := exchange(t, serverCfg, clientCfg, "node-a")
	if after.serverErr != nil || after.clientErr != nil {
		t.Fatalf("handshake after renewal failed: server=%v client=%v", after.serverErr, after.clientErr)
	}
	if after.clientSawServer.Cmp(first.Leaf.SerialNumber) == 0 {
		t.Fatal("handshake after renewal still presented the old certificate")
	}
}

func TestExistingConnectionUnaffectedByRenewal(t *testing.T) {
	ca := mustCA(t, testSecret)
	clk := NewManualClock(t0)
	issA := mustIssuer(t, ca, "node-a", IssuerOptions{Clock: clk})
	idB := mustIssuer(t, ca, "node-b", IssuerOptions{Clock: clk}).Identity()
	serverCfg, err := ServerConfig(issA.Identity())
	if err != nil {
		t.Fatal(err)
	}
	clientCfg, err := ClientConfig(idB)
	if err != nil {
		t.Fatal(err)
	}

	srv, cli, serr, cerr := connect(t, serverCfg, clientCfg, "node-a")
	if serr != nil || cerr != nil {
		t.Fatalf("handshake failed: server=%v client=%v", serr, cerr)
	}
	oldSerial := cli.ConnectionState().PeerCertificates[0].SerialNumber

	// Move past the original certificate's expiry. The renewals have happened,
	// and the old connection carries on without re-checking its certificate.
	clk.Advance(DefaultLifetime + time.Hour)
	if err := roundTrip(cli, srv); err != nil {
		t.Fatalf("existing connection broken by renewal: %v", err)
	}

	srv2, cli2, serr, cerr := connect(t, serverCfg, clientCfg, "node-a")
	if serr != nil || cerr != nil {
		t.Fatalf("new handshake failed: server=%v client=%v", serr, cerr)
	}
	if cli2.ConnectionState().PeerCertificates[0].SerialNumber.Cmp(oldSerial) == 0 {
		t.Fatal("new handshake presented the expired certificate")
	}
	if err := roundTrip(cli2, srv2); err != nil {
		t.Fatalf("new connection failed: %v", err)
	}
}

func TestCertificateFromOtherCAIsRejectedAtClock(t *testing.T) {
	ca := mustCA(t, testSecret)
	other := mustCA(t, []byte("ffffffffffffffffffffffffffffffff"))
	clk := NewManualClock(t0)
	idA := mustIssuer(t, ca, "node-a", IssuerOptions{Clock: clk}).Identity()
	idX := mustIssuer(t, other, "node-x", IssuerOptions{Clock: clk}).Identity()
	serverCfg, err := ServerConfig(idA)
	if err != nil {
		t.Fatal(err)
	}
	clientCfg, err := ClientConfig(idX)
	if err != nil {
		t.Fatal(err)
	}
	res := exchange(t, serverCfg, clientCfg, "node-a")
	if res.serverErr == nil && res.clientErr == nil {
		t.Fatal("certificate from another CA accepted")
	}
}

func TestCertificateFastPathDoesNotAllocate(t *testing.T) {
	ca := mustCA(t, testSecret)
	is := mustIssuer(t, ca, "node-a", IssuerOptions{Clock: NewManualClock(t0)})
	if allocs := testing.AllocsPerRun(200, func() { _ = is.Certificate() }); allocs != 0 {
		t.Fatalf("Certificate allocates %v times per call on the handshake path", allocs)
	}
}

func BenchmarkIssuerCertificate(b *testing.B) {
	ca, err := DeriveCA(testSecret)
	if err != nil {
		b.Fatal(err)
	}
	is, err := ca.NewIssuer("node-a", nil, IssuerOptions{})
	if err != nil {
		b.Fatal(err)
	}
	defer func() { _ = is.Close() }()
	b.ReportAllocs()
	for b.Loop() {
		_ = is.Certificate()
	}
}
