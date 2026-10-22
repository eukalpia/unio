package policy

// globMatch reports whether s matches pat, where '*' matches any run of
// bytes (including none) and '?' matches exactly one byte. When fold is set,
// ASCII letters compare case-insensitively.
//
// The matcher is iterative and keeps only the position of the last '*'
// (the classic greedy algorithm), so it never backtracks exponentially and
// its cost is bounded by len(pat)*len(s). Policy size limits keep both
// small. Recursion is avoided on purpose.
func globMatch(pat, s string, fold bool) bool {
	pi, si := 0, 0
	starP, starS := -1, 0
	for si < len(s) {
		if pi < len(pat) {
			c := pat[pi]
			if c == '*' {
				starP, starS = pi, si
				pi++
				continue
			}
			if c == '?' || byteEqual(c, s[si], fold) {
				pi++
				si++
				continue
			}
		}
		if starP < 0 {
			return false
		}
		// Let the last '*' absorb one more byte and retry from there.
		starS++
		pi, si = starP+1, starS
	}
	for pi < len(pat) && pat[pi] == '*' {
		pi++
	}
	return pi == len(pat)
}

// byteEqual compares two bytes, folding ASCII case when fold is set.
func byteEqual(a, b byte, fold bool) bool {
	if a == b {
		return true
	}
	if !fold {
		return false
	}
	return toLower(a) == toLower(b)
}

// toLower maps ASCII 'A'..'Z' to lower case and leaves other bytes alone.
func toLower(c byte) byte {
	if 'A' <= c && c <= 'Z' {
		return c + ('a' - 'A')
	}
	return c
}
