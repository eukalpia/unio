package pki

import (
	"context"
	"crypto/tls"
	"io"
	"math/big"
	"net"
	"testing"
	"time"

	"github.com/eukalpia/unio/internal/ids"
)

// dnsOf is the DNS identity a node's certificate must carry, written out here
// so that the tests pin the wire contract rather than the implementation.
func dnsOf(id ids.NodeID) string {
	return string(id) + ".node.unio"
}

// handshakeResult records what each side saw after a mutual TLS exchange.
type handshakeResult struct {
	serverPeer ids.NodeID
	clientPeer ids.NodeID
	serverErr  error
	clientErr  error
	// clientSawServer and serverSawClient are the serial numbers of the leaf
	// certificates each side received. They show which certificate a
	// handshake used.
	clientSawServer *big.Int
	serverSawClient *big.Int
}

// connect performs one mutual TLS handshake over loopback. The client dials
// node to: its ServerName is that node's DNS identity. An empty to leaves the
// configuration as given, so that a test can check what a missing ServerName
// does. It returns both ends and each side's handshake error. Connections are
// closed when the test ends.
func connect(t *testing.T, serverCfg, clientCfg *tls.Config, to ids.NodeID) (srv, cli *tls.Conn, serverErr, clientErr error) {
	t.Helper()
	cfg := clientCfg.Clone()
	if to != "" {
		cfg.ServerName = dnsOf(to)
	}
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = ln.Close() }()

	type accepted struct {
		conn *tls.Conn
		err  error
	}
	acc := make(chan accepted, 1)
	go func() {
		nc, err := ln.Accept()
		if err != nil {
			acc <- accepted{err: err}
			return
		}
		conn := tls.Server(nc, serverCfg)
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := conn.HandshakeContext(ctx); err != nil {
			_ = nc.Close()
			acc <- accepted{err: err}
			return
		}
		acc <- accepted{conn: conn}
	}()

	nc, err := net.Dial("tcp", ln.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	cli = tls.Client(nc, cfg)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	clientErr = cli.HandshakeContext(ctx)
	cancel()
	if clientErr != nil {
		// Close the client side so the server sees the end at once, instead of
		// waiting out its handshake timeout.
		_ = nc.Close()
	}
	s := <-acc
	srv, serverErr = s.conn, s.err
	t.Cleanup(func() {
		_ = cli.Close()
		if srv != nil {
			_ = srv.Close()
		}
	})
	return srv, cli, serverErr, clientErr
}

// roundTrip sends one byte each way over an established connection.
func roundTrip(cli, srv *tls.Conn) error {
	deadline := time.Now().Add(5 * time.Second)
	_ = cli.SetDeadline(deadline)
	_ = srv.SetDeadline(deadline)
	if _, err := cli.Write([]byte{'q'}); err != nil {
		return err
	}
	buf := make([]byte, 1)
	if _, err := io.ReadFull(srv, buf); err != nil {
		return err
	}
	if _, err := srv.Write([]byte{'p'}); err != nil {
		return err
	}
	_, err := io.ReadFull(cli, buf)
	return err
}

// exchange handshakes with the client dialing node to, then performs a round
// trip, and reports the outcome on both sides.
func exchange(t *testing.T, serverCfg, clientCfg *tls.Config, to ids.NodeID) handshakeResult {
	t.Helper()
	srv, cli, serr, cerr := connect(t, serverCfg, clientCfg, to)
	res := handshakeResult{serverErr: serr, clientErr: cerr}
	if serr != nil || cerr != nil {
		return res
	}
	res.serverPeer, res.serverErr = PeerID(srv.ConnectionState())
	res.clientPeer, res.clientErr = PeerID(cli.ConnectionState())
	res.clientSawServer = cli.ConnectionState().PeerCertificates[0].SerialNumber
	res.serverSawClient = srv.ConnectionState().PeerCertificates[0].SerialNumber
	if res.serverErr == nil && res.clientErr == nil {
		res.clientErr = roundTrip(cli, srv)
	}
	return res
}

func TestSameSecretNodesTrustEachOther(t *testing.T) {
	caA := mustCA(t, testSecret)
	caB := mustCA(t, testSecret)
	idA := mustIdentity(t, caA, "node-a")
	idB := mustIdentity(t, caB, "node-b")

	serverCfg, err := ServerConfig(idA)
	if err != nil {
		t.Fatal(err)
	}
	clientCfg, err := ClientConfig(idB)
	if err != nil {
		t.Fatal(err)
	}
	res := exchange(t, serverCfg, clientCfg, "node-a")
	if res.serverErr != nil || res.clientErr != nil {
		t.Fatalf("exchange failed: server=%v client=%v", res.serverErr, res.clientErr)
	}
	if res.serverPeer != "node-b" {
		t.Fatalf("server saw peer %q, want node-b", res.serverPeer)
	}
	if res.clientPeer != "node-a" {
		t.Fatalf("client saw peer %q, want node-a", res.clientPeer)
	}
}

func TestDifferentSecretRejected(t *testing.T) {
	caA := mustCA(t, testSecret)
	caOther := mustCA(t, []byte("ffffffffffffffffffffffffffffffff"))
	idA := mustIdentity(t, caA, "node-a")
	idIntruder := mustIdentity(t, caOther, "node-x")

	serverCfg, err := ServerConfig(idA)
	if err != nil {
		t.Fatal(err)
	}
	clientCfg, err := ClientConfig(idIntruder)
	if err != nil {
		t.Fatal(err)
	}
	res := exchange(t, serverCfg, clientCfg, "node-a")
	if res.serverErr == nil && res.clientErr == nil {
		t.Fatal("certificate from a different cluster secret was accepted")
	}
}

func TestServerRejectsForeignClientButClientRejectsForeignServer(t *testing.T) {
	caA := mustCA(t, testSecret)
	caOther := mustCA(t, []byte("ffffffffffffffffffffffffffffffff"))
	idA := mustIdentity(t, caA, "node-a")
	idOther := mustIdentity(t, caOther, "node-x")

	// Foreign server: the client must refuse it.
	serverCfg, err := ServerConfig(idOther)
	if err != nil {
		t.Fatal(err)
	}
	clientCfg, err := ClientConfig(idA)
	if err != nil {
		t.Fatal(err)
	}
	if res := exchange(t, serverCfg, clientCfg, "node-x"); res.clientErr == nil {
		t.Fatal("client accepted a server certificate from a foreign CA")
	}
}

// TestServerCertificateCannotBeUsedForAnotherNode checks that a valid server
// certificate for node A is refused by a client that dialed node B. The chain
// and the validity are fine, so only the identity can refuse it.
func TestServerCertificateCannotBeUsedForAnotherNode(t *testing.T) {
	ca := mustCA(t, testSecret)
	idA := mustIdentity(t, ca, "node-a")
	idC := mustIdentity(t, ca, "node-c")
	serverCfg, err := ServerConfig(idA)
	if err != nil {
		t.Fatal(err)
	}
	clientCfg, err := ClientConfig(idC)
	if err != nil {
		t.Fatal(err)
	}
	if res := exchange(t, serverCfg, clientCfg, "node-a"); res.serverErr != nil || res.clientErr != nil {
		t.Fatalf("dialing the right node failed: server=%v client=%v", res.serverErr, res.clientErr)
	}
	res := exchange(t, serverCfg, clientCfg, "node-b")
	if res.clientErr == nil && res.serverErr == nil {
		t.Fatal("node A's certificate was accepted when dialing node B")
	}
}

// TestClientRequiresServerName checks that a client configuration without a
// ServerName cannot complete a handshake. Without one, the standard checks
// cannot bind the certificate to a node.
func TestClientRequiresServerName(t *testing.T) {
	ca := mustCA(t, testSecret)
	idA := mustIdentity(t, ca, "node-a")
	idB := mustIdentity(t, ca, "node-b")
	serverCfg, err := ServerConfig(idA)
	if err != nil {
		t.Fatal(err)
	}
	clientCfg, err := ClientConfig(idB)
	if err != nil {
		t.Fatal(err)
	}
	if res := exchange(t, serverCfg, clientCfg, ""); res.serverErr == nil && res.clientErr == nil {
		t.Fatal("handshake completed without a ServerName")
	}
}

func TestExpiredPeerRejected(t *testing.T) {
	ca := mustCA(t, testSecret)
	idA := mustIdentity(t, ca, "node-a")
	now := time.Now()
	expired, err := ca.issueAt("node-b", nil, now.Add(-48*time.Hour), now.Add(-time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	serverCfg, err := ServerConfig(idA)
	if err != nil {
		t.Fatal(err)
	}
	clientCfg, err := ClientConfig(&Identity{fixed: &expired, Roots: ca.CertPool(), clock: SystemClock()})
	if err != nil {
		t.Fatal(err)
	}
	res := exchange(t, serverCfg, clientCfg, "node-a")
	if res.serverErr == nil && res.clientErr == nil {
		t.Fatal("expired client certificate was accepted")
	}
}

func TestConfigsAreTLS13Only(t *testing.T) {
	ca := mustCA(t, testSecret)
	id := mustIdentity(t, ca, "node-a")
	server, err := ServerConfig(id)
	if err != nil {
		t.Fatal(err)
	}
	client, err := ClientConfig(id)
	if err != nil {
		t.Fatal(err)
	}
	for name, cfg := range map[string]*tls.Config{"server": server, "client": client} {
		if cfg.MinVersion != tls.VersionTLS13 || cfg.MaxVersion != tls.VersionTLS13 {
			t.Fatalf("%s config versions = %x..%x, want TLS 1.3 only", name, cfg.MinVersion, cfg.MaxVersion)
		}
		if !cfg.SessionTicketsDisabled {
			t.Fatalf("%s config enables session tickets, which would skip peer checks on resume", name)
		}
	}
	if server.GetCertificate == nil || client.GetClientCertificate == nil {
		t.Fatal("configs do not serve the identity's current certificate")
	}
}

// TestConfigsVerifyWithStandardChecks checks that neither configuration turns
// off the standard certificate verification, and that both verify against the
// identity's clock. A configuration that skipped verification would accept any
// certificate.
func TestConfigsVerifyWithStandardChecks(t *testing.T) {
	ca := mustCA(t, testSecret)
	id := mustIdentity(t, ca, "node-a")
	server, err := ServerConfig(id)
	if err != nil {
		t.Fatal(err)
	}
	client, err := ClientConfig(id)
	if err != nil {
		t.Fatal(err)
	}
	for name, cfg := range map[string]*tls.Config{"server": server, "client": client} {
		if cfg.InsecureSkipVerify {
			t.Fatalf("%s config skips certificate verification", name)
		}
		if cfg.Time == nil {
			t.Fatalf("%s config verifies against the wall clock, not the identity's clock", name)
		}
		if cfg.VerifyConnection == nil {
			t.Fatalf("%s config has no node identity check", name)
		}
	}
	if server.ClientAuth != tls.RequireAndVerifyClientCert {
		t.Fatalf("server client auth = %v, want RequireAndVerifyClientCert", server.ClientAuth)
	}
	if client.RootCAs == nil || server.ClientCAs == nil {
		t.Fatal("configs do not trust the cluster roots")
	}
}

func TestTLS12ClientRejected(t *testing.T) {
	ca := mustCA(t, testSecret)
	idA := mustIdentity(t, ca, "node-a")
	idB := mustIdentity(t, ca, "node-b")
	serverCfg, err := ServerConfig(idA)
	if err != nil {
		t.Fatal(err)
	}
	clientCfg, err := ClientConfig(idB)
	if err != nil {
		t.Fatal(err)
	}
	clientCfg.MinVersion = tls.VersionTLS12
	clientCfg.MaxVersion = tls.VersionTLS12
	res := exchange(t, serverCfg, clientCfg, "node-a")
	if res.serverErr == nil && res.clientErr == nil {
		t.Fatal("TLS 1.2 handshake was accepted")
	}
}
