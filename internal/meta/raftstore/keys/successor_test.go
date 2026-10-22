package keys

import (
	"bytes"
	"testing"
)

func TestSuccessor(t *testing.T) {
	tests := []struct {
		name    string
		in      string
		want    string
		wantNil bool
	}{
		{name: "empty", in: "", wantNil: true},
		{name: "all ff", in: "ff ff", wantNil: true},
		{name: "single byte", in: "61", want: "62"},
		{name: "trailing ff dropped", in: "61 ff ff", want: "62"},
		{name: "nul", in: "00", want: "01"},
		{name: "ff then nul", in: "ff 00", want: "ff 01"},
		{name: "carry past trailing nul ff", in: "61 00 ff", want: "61 01"},
		{name: "high byte", in: "7f ff", want: "80"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := Successor(mustHex(tt.in))
			if tt.wantNil {
				if got != nil {
					t.Errorf("Successor(%s) = %x, want nil", tt.in, got)
				}
				return
			}
			if want := mustHex(tt.want); !bytes.Equal(got, want) {
				t.Errorf("Successor(%s) = %x, want %x", tt.in, got, want)
			}
		})
	}
}

func TestSuccessorDoesNotAlias(t *testing.T) {
	in := mustHex("61 ff")
	s := Successor(in)
	s[0] = 0
	if in[0] != 0x61 {
		t.Errorf("Successor modified its input: %x", in)
	}
}

// allFF reports whether b is non-empty and consists only of 0xFF bytes, or
// is empty; both cases have no successor.
func allFF(b []byte) bool {
	for _, c := range b {
		if c != 0xff {
			return false
		}
	}
	return true
}

// FuzzSuccessor checks that Successor(b) is strictly greater than b and than
// every extension of b, and that it is nil only for empty or all-0xFF input.
func FuzzSuccessor(f *testing.F) {
	f.Add([]byte("a"), []byte{0x00})
	f.Add([]byte{0xff}, []byte{0x01})
	f.Add([]byte{0x61, 0xff}, []byte{0xff, 0xff})
	f.Fuzz(func(t *testing.T, b, ext []byte) {
		s := Successor(b)
		if s == nil {
			if !(len(b) == 0 || allFF(b)) {
				t.Fatalf("Successor(%x) = nil, want a successor", b)
			}
			return
		}
		if len(b) == 0 || allFF(b) {
			t.Fatalf("Successor(%x) = %x, want nil", b, s)
		}
		if bytes.Compare(s, b) <= 0 {
			t.Fatalf("Successor(%x) = %x is not greater than its input", b, s)
		}
		extended := append(bytes.Clone(b), ext...)
		if bytes.Compare(extended, s) >= 0 {
			t.Fatalf("extension %x of %x is not below Successor %x", extended, b, s)
		}
	})
}
