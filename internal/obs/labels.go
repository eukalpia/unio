package obs

// maxLabelLen is the longest label value kept as given.
const maxLabelLen = 64

// otherLabel replaces every label value that is rejected by sanitizeLabel.
const otherLabel = "other"

// sanitizeLabel returns v unchanged when it is a bounded, safe label value:
// non-empty, at most maxLabelLen bytes, and made only of [A-Za-z0-9_.:-].
// Anything else becomes otherLabel. Without this guard a caller could create
// unbounded series from user input, which is a denial-of-service vector.
func sanitizeLabel(v string) string {
	if v == "" || len(v) > maxLabelLen {
		return otherLabel
	}
	for i := range len(v) {
		if !isLabelByte(v[i]) {
			return otherLabel
		}
	}
	return v
}

// isLabelByte reports whether c is allowed in a label value.
func isLabelByte(c byte) bool {
	switch {
	case 'a' <= c && c <= 'z', 'A' <= c && c <= 'Z', '0' <= c && c <= '9':
		return true
	case c == '_', c == '.', c == ':', c == '-':
		return true
	}
	return false
}
