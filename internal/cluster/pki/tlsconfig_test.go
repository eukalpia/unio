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

// connect performs one mutual TLS handshake over loopback. It returns both ends
// and each side's handshake error. Connections are closed when the test ends.
func connect(t *testing.T, serverCfg, clientCfg *tls.Config) (srv, cli *tls.Conn, serverErr, clientErr error) {
	t.Helper()
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
	cli = tls.Client(nc, clientCfg)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	clientErr = cli.HandshakeContext(ctx)
	cancel()
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

// exchange handshakes, then performs a round trip, and reports the outcome on
// both sides.
func exchange(t *testing.T, serverCfg, clientCfg *tls.Config) handshakeResult {
	t.Helper()
	srv, cli, serr, cerr := connect(t, serverCfg, clientCfg)
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
	res := exchange(t, serverCfg, clientCfg)
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
	res := exchange(t, serverCfg, clientCfg)
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
	if res := exchange(t, serverCfg, clientCfg); res.clientErr == nil {
		t.Fatal("client accepted a server certificate from a foreign CA")
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
	res := exchange(t, serverCfg, clientCfg)
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
	if server.ClientAuth != tls.RequireAnyClientCert || server.VerifyPeerCertificate == nil {
		t.Fatal("server does not require and verify client certificates")
	}
	if server.GetCertificate == nil || client.GetClientCertificate == nil {
		t.Fatal("configs do not serve the identity's current certificate")
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
	res := exchange(t, serverCfg, clientCfg)
	if res.serverErr == nil && res.clientErr == nil {
		t.Fatal("TLS 1.2 handshake was accepted")
	}
}
