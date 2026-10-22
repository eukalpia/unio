package admin

import (
	"context"
	"net/url"
	"strings"
	"testing"

	"github.com/eukalpia/unio/internal/auth/authtest"
	"github.com/eukalpia/unio/internal/auth/iam"
	"github.com/eukalpia/unio/internal/s3err"
)

// TestUserNameRuleMatchesIAM pins the local copy of the IAM user name rule. The
// IAM rule is unexported, so the test asks the IAM store itself: a name the store
// accepts must pass validUserName, and a name it refuses with InvalidArgument
// must fail it.
func TestUserNameRuleMatchesIAM(t *testing.T) {
	store, err := iam.New(authtest.NewMemKV(), iam.Config{
		KEK:           iam.DeriveKEK([]byte(testSecret)),
		RootAccessKey: testRoot,
		RootSecret:    testRootSecret,
	})
	if err != nil {
		t.Fatal(err)
	}
	names := []string{
		"alice", "a", "A9", "9lives", "bob.smith", "ops_team", "dev@corp", "x-y",
		"", ".hidden", "_under", "@at", "-dash", "trailing.", "space name", "slash/name",
		"tab\tname", "unié", "semi;colon", "percent%20", "question?",
		strings.Repeat("a", 64), strings.Repeat("a", 65), strings.Repeat("b", 63) + ".",
	}
	for _, name := range names {
		_, err := store.CreateUser(context.Background(), name)
		storeAccepts := err == nil
		if err != nil && !s3err.Is(err, s3err.InvalidArgument) {
			t.Fatalf("CreateUser(%q): unexpected error %v", name, err)
		}
		if got := validUserName(name); got != storeAccepts {
			t.Errorf("validUserName(%q) = %v, but the IAM store accepts it = %v", name, got, storeAccepts)
		}
	}
}

func TestValidHost(t *testing.T) {
	good := []string{"localhost", "unio.example:9000", "10.0.0.1:9000", "[2001:db8::1]:9000", "s3-1.internal"}
	bad := []string{"", "a b", "host/path", "host?x", "host#x", "user@host", "a\nb", "[::1", strings.Repeat("a", 256)}
	for _, h := range good {
		if !validHost(h) {
			t.Errorf("validHost(%q) = false", h)
		}
	}
	for _, h := range bad {
		if validHost(h) {
			t.Errorf("validHost(%q) = true", h)
		}
	}
}

func TestListLimitParam(t *testing.T) {
	cases := []struct {
		query string
		want  int
		bad   bool
	}{
		{"", 200, false},
		{"max=1", 1, false},
		{"max=1000", 1000, false},
		{"max=0", 0, true},
		{"max=1001", 0, true},
		{"max=-1", 0, true},
		{"max=%2B5", 0, true},
		{"max=abc", 0, true},
		{"max=", 0, true},
		{"max=5&max=6", 0, true},
		{"max=99999999999999999999999", 0, true},
	}
	for _, tc := range cases {
		q, err := url.ParseQuery(tc.query)
		if err != nil {
			t.Fatal(err)
		}
		got, err := listLimitParam(q)
		if tc.bad {
			if err == nil {
				t.Errorf("max %q accepted as %d", tc.query, got)
			}
			continue
		}
		if err != nil || got != tc.want {
			t.Errorf("max %q = %d, %v; want %d", tc.query, got, err, tc.want)
		}
	}
}
