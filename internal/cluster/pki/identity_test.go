package pki

import (
	"crypto/ed25519"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"math/big"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func writeOperatorFiles(t *testing.T, dir string, certDER []byte, key any, caPEM []byte) (certFile, keyFile, caFile string) {
	t.Helper()
	keyDER, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		t.Fatal(err)
	}
	certFile = filepath.Join(dir, "node.crt")
	keyFile = filepath.Join(dir, "node.key")
	caFile = filepath.Join(dir, "ca.crt")
	if err := os.WriteFile(certFile, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: certDER}), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(keyFile, pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: keyDER}), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(caFile, caPEM, 0o600); err != nil {
		t.Fatal(err)
	}
	return certFile, keyFile, caFile
}

func TestLoadIdentityFromOperatorFiles(t *testing.T) {
	ca := mustCA(t, testSecret)
	cert, err := ca.IssueNode("node-op", []string{"127.0.0.1"})
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	certFile, keyFile, caFile := writeOperatorFiles(t, dir, cert.Certificate[0], cert.PrivateKey, ca.PEM())

	loaded, err := LoadIdentity(certFile, keyFile, caFile)
	if err != nil {
		t.Fatal(err)
	}
	got, err := nodeIDFromCert(currentCert(t, loaded).Leaf)
	if err != nil || got != "node-op" {
		t.Fatalf("loaded identity id = %q, %v; want node-op", got, err)
	}

	peer := mustIdentity(t, ca, "node-peer")
	serverCfg, err := ServerConfig(loaded)
	if err != nil {
		t.Fatal(err)
	}
	clientCfg, err := ClientConfig(peer)
	if err != nil {
		t.Fatal(err)
	}
	if res := exchange(t, serverCfg, clientCfg, "node-op"); res.serverErr != nil || res.clientErr != nil {
		t.Fatalf("operator identity failed to interoperate: server=%v client=%v", res.serverErr, res.clientErr)
	}
}

// TestLoadIdentityCleansPaths loads files through a path that detours through a
// directory that does not exist. It loads only if the path is cleaned first.
func TestLoadIdentityCleansPaths(t *testing.T) {
	ca := mustCA(t, testSecret)
	cert, err := ca.IssueNode("node-clean", nil)
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	certFile, keyFile, caFile := writeOperatorFiles(t, dir, cert.Certificate[0], cert.PrivateKey, ca.PEM())
	// filepath.Join would clean the detour away before LoadIdentity sees it, so
	// the path is built by hand.
	detour := func(path string) string {
		sep := string(filepath.Separator)
		return filepath.Dir(path) + sep + "missing" + sep + ".." + sep + filepath.Base(path)
	}
	if _, err := LoadIdentity(detour(certFile), detour(keyFile), detour(caFile)); err != nil {
		t.Fatalf("cleaned path rejected: %v", err)
	}
}

func TestLoadIdentityErrors(t *testing.T) {
	ca := mustCA(t, testSecret)
	cert, err := ca.IssueNode("node-op", nil)
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()

	t.Run("missing files", func(t *testing.T) {
		if _, err := LoadIdentity(filepath.Join(dir, "absent.crt"), filepath.Join(dir, "absent.key"), filepath.Join(dir, "absent-ca.crt")); err == nil {
			t.Fatal("missing files accepted")
		}
	})

	t.Run("CA file without certificates", func(t *testing.T) {
		certFile, keyFile, _ := writeOperatorFiles(t, dir, cert.Certificate[0], cert.PrivateKey, ca.PEM())
		bad := filepath.Join(dir, "empty-ca.crt")
		if err := os.WriteFile(bad, []byte("not a certificate"), 0o600); err != nil {
			t.Fatal(err)
		}
		if _, err := LoadIdentity(certFile, keyFile, bad); err == nil {
			t.Fatal("CA file with no certificates accepted")
		}
	})

	t.Run("certificate without node URI", func(t *testing.T) {
		key := cert.PrivateKey.(ed25519.PrivateKey)
		serial, err := rand.Int(rand.Reader, big.NewInt(1<<62))
		if err != nil {
			t.Fatal(err)
		}
		tmpl := &x509.Certificate{
			SerialNumber: serial,
			Subject:      pkix.Name{CommonName: "plain"},
			NotBefore:    time.Now().Add(-time.Minute),
			NotAfter:     time.Now().Add(time.Hour),
			KeyUsage:     x509.KeyUsageDigitalSignature,
			ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth, x509.ExtKeyUsageClientAuth},
		}
		der, err := x509.CreateCertificate(rand.Reader, tmpl, ca.Certificate(), key.Public(), ca.key)
		if err != nil {
			t.Fatal(err)
		}
		certFile, keyFile, caFile := writeOperatorFiles(t, dir, der, key, ca.PEM())
		if _, err := LoadIdentity(certFile, keyFile, caFile); err == nil {
			t.Fatal("certificate without a unio node URI accepted")
		}
	})
}
