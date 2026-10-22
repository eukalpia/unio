package iam

import (
	"strings"
	"testing"
)

// FuzzValidUserName checks that every accepted name stays inside its own key
// segment of the SystemKV layout, so a name can never address another
// record.
func FuzzValidUserName(f *testing.F) {
	f.Add("alice")
	f.Add("bob.smith@example")
	f.Add("../keys/AAAA")
	f.Add("a/b")
	f.Add("")
	f.Fuzz(func(t *testing.T, name string) {
		if !validUserName(name) {
			return
		}
		if n := strings.Count(userPath(name), "/"); n != 2 {
			t.Fatalf("accepted name %q yields key %q with %d separators", name, userPath(name), n)
		}
		if len(name) > maxUserNameLen || !isAlnum(name[0]) {
			t.Fatalf("accepted malformed name %q", name)
		}
	})
}

// FuzzDecodeRecords feeds arbitrary bytes to every record decoder. None may
// panic, and an accepted record must carry the current version.
func FuzzDecodeRecords(f *testing.F) {
	f.Add([]byte(`{"v":1,"id":"x","name":"n","created":"2026-10-22T12:00:00Z","enabled":true}`))
	f.Add([]byte(`{"v":1,"user":"u","userId":"x","created":"2026-10-22T12:00:00Z","secret":"AAAA"}`))
	f.Add([]byte(`{"v":1,"userId":"x","document":"{}"}`))
	f.Add([]byte(`{"v":2}`))
	f.Add([]byte(`null`))
	f.Fuzz(func(t *testing.T, b []byte) {
		if r, err := decodeUserRecord(b); err == nil && r.V != recordVersion {
			t.Fatalf("accepted user record of version %d", r.V)
		}
		if r, err := decodeKeyRecord(b); err == nil && r.V != recordVersion {
			t.Fatalf("accepted key record of version %d", r.V)
		}
		if r, err := decodePolicyRecord(b); err == nil && r.V != recordVersion {
			t.Fatalf("accepted policy record of version %d", r.V)
		}
	})
}
