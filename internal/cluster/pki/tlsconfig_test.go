package pki

import (
	"context"
	"crypto/tls"
	"io"
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
}

// exchange runs one loopback connection: the client sends a byte, the server
// answers with another, and each side reports its peer id. Any failure in the
// handshake or the round trip is returned.
func exchange(t *testing.T, serverCfg, clientCfg *tls.Config) handshakeResult {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()

	var res handshakeResult
	done := make(chan struct{})
	go func() {
		defer close(done)
		nc, err := ln.Accept()
		if err != nil {
			res.serverErr = err
			return
		}
		defer nc.Close()
		conn := tls.Server(nc, serverCfg)
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := conn.HandshakeContext(ctx); err != nil {
			res.serverErr = err
			return
		}
		res.serverPeer, res.serverErr = PeerID(conn.ConnectionState())
		if res.serverErr != nil {
			return
		}
		buf := make([]byte, 1)
		if _, err := io.ReadFull(conn, buf); err != nil {
			res.serverErr = err
			return
		}
		_, res.serverErr = conn.Write([]byte{'p'})
	}()

	nc, err := net.Dial("tcp", ln.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	defer nc.Close()
	conn := tls.Client(nc, clientCfg)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := conn.HandshakeContext(ctx); err != nil {
		res.clientErr = err
	} else {
		res.clientPeer, res.clientErr = PeerID(conn.ConnectionState())
		if res.clientErr == nil {
			_, res.clientErr = conn.Write([]byte{'q'})
		}
		if res.clientErr == nil {
			buf := make([]byte, 1)
			_, res.clientErr = io.ReadFull(conn, buf)
		}
	}
	<-done
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
	clientCfg, err := ClientConfig(&Identity{Certificate: expired, Roots: ca.CertPool()})
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
	if server.ClientAuth != tls.RequireAndVerifyClientCert {
		t.Fatal("server does not require client certificates")
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
