package uniwire

import (
	"errors"
	"testing"
	"time"

	uniov1 "github.com/eukalpia/unio/internal/pb/unio/v1"
)

// TestCertificateForAnotherNodeIsRefused dials node b at the address of node a.
// Node a presents a valid certificate for its own identity, which the dialer
// must refuse because it asked for b.
func TestCertificateForAnotherNodeIsRefused(t *testing.T) {
	n := newTestNet(t)
	a := n.node("a", Limits{})
	if err := a.Register("test.Echo", echoHandler); err != nil {
		t.Fatal(err)
	}
	dialer := n.dialOnly("c", Limits{})
	n.setAddr("b", n.addrOf(a))
	var resp uniov1.DriveRequest
	err := dialer.Call(ctxWithTimeout(t, 5*time.Second), "b", "test.Echo", &uniov1.DriveRequest{}, &resp)
	if !errors.Is(err, ErrUnavailable) {
		t.Fatalf("call to node b answered by node a's certificate: %v, want ErrUnavailable", err)
	}
}
