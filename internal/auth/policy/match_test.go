package policy

import (
	"strings"
	"testing"
	"time"
)

// refGlob is a quadratic dynamic-programming reference for globMatch. It is
// obviously correct and slow, so tests compare the fast matcher with it.
func refGlob(pat, s string, fold bool) bool {
	n, m := len(pat), len(s)
	dp := make([][]bool, n+1)
	for i := range dp {
		dp[i] = make([]bool, m+1)
	}
	dp[0][0] = true
	for i := 1; i <= n; i++ {
		if pat[i-1] == '*' {
			dp[i][0] = dp[i-1][0]
		}
	}
	for i := 1; i <= n; i++ {
		for j := 1; j <= m; j++ {
			switch c := pat[i-1]; {
			case c == '*':
				dp[i][j] = dp[i-1][j] || dp[i][j-1]
			case c == '?':
				dp[i][j] = dp[i-1][j-1]
			default:
				dp[i][j] = dp[i-1][j-1] && byteEqual(c, s[j-1], fold)
			}
		}
	}
	return dp[n][m]
}

func TestGlobMatch(t *testing.T) {
	cases := []struct {
		pat, s string
		fold   bool
		want   bool
	}{
		{"", "", false, true},
		{"", "a", false, false},
		{"a", "", false, false},
		{"*", "", false, true},
		{"*", "anything/at/all", false, true},
		{"**", "x", false, true},
		{"?", "", false, false},
		{"?", "x", false, true},
		{"a*", "abc", false, true},
		{"*c", "abc", false, true},
		{"a*c", "abc", false, true},
		{"a*b*c", "axxbyyc", false, true},
		{"a*b*c", "axxbyy", false, false},
		{"s3:Get*", "s3:GetObject", true, true},
		{"s3:get*", "S3:GETOBJECT", true, true},
		{"s3:get*", "S3:GETOBJECT", false, false},
		{"arn:aws:s3:::b/*", "arn:aws:s3:::b/k/v", false, true},
		{"arn:aws:s3:::b", "arn:aws:s3:::b/k", false, false},
		{"a?c", "abc", false, true},
		{"a?c", "ac", false, false},
		{"*?*", "", false, false},
		{"*?*", "z", false, true},
		{"a*a", "a", false, false},
		{"*/*", "photos/a/b.jpg", false, true},
	}
	for _, c := range cases {
		if got := globMatch(c.pat, c.s, c.fold); got != c.want {
			t.Fatalf("globMatch(%q, %q, fold=%v) = %v, want %v", c.pat, c.s, c.fold, got, c.want)
		}
		if got := refGlob(c.pat, c.s, c.fold); got != c.want {
			t.Fatalf("reference disagrees on %q/%q: %v", c.pat, c.s, got)
		}
	}
}

func TestGlobMatchAgreesWithReference(t *testing.T) {
	alphabet := []string{"a", "b", "/", "*", "?"}
	var pats, subs []string
	var build func(prefix string, depth int, out *[]string)
	build = func(prefix string, depth int, out *[]string) {
		*out = append(*out, prefix)
		if depth == 0 {
			return
		}
		for _, c := range alphabet {
			build(prefix+c, depth-1, out)
		}
	}
	build("", 4, &pats)
	build("", 4, &subs)
	subs = filterOut(subs, "*?")
	for _, p := range pats {
		for _, s := range subs {
			if globMatch(p, s, false) != refGlob(p, s, false) {
				t.Fatalf("mismatch for pattern %q subject %q", p, s)
			}
		}
	}
}

// filterOut drops subjects that contain wildcard characters: subjects are
// literal strings in the policy engine, never patterns.
func filterOut(in []string, chars string) []string {
	var out []string
	for _, s := range in {
		if !strings.ContainsAny(s, chars) {
			out = append(out, s)
		}
	}
	return out
}

// TestGlobMatchIsNotExponential checks a pattern built to defeat naive
// recursive matchers. It must finish in well under the test timeout.
func TestGlobMatchIsNotExponential(t *testing.T) {
	pat := strings.Repeat("a*", 60) + "b"
	subject := strings.Repeat("a", 4000)
	start := time.Now()
	if globMatch(pat, subject, false) {
		t.Fatal("pathological pattern matched")
	}
	if d := time.Since(start); d > 5*time.Second {
		t.Fatalf("matching took %v", d)
	}
}
