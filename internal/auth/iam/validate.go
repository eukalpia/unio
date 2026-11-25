package iam

// maxUserNameLen bounds the length of an IAM user name.
const maxUserNameLen = 64

// validUserName reports whether name is an acceptable user name: 1 to 64
// characters, starting with a letter or digit and continuing with letters,
// digits or ._@-. The name becomes part of a SystemKV key, so '/' and other
// separators are excluded.
func validUserName(name string) bool {
	if len(name) == 0 || len(name) > maxUserNameLen {
		return false
	}
	for i := 0; i < len(name); i++ {
		c := name[i]
		if isAlnum(c) {
			continue
		}
		if i > 0 && (c == '.' || c == '_' || c == '@' || c == '-') {
			continue
		}
		return false
	}
	return true
}

// isAlnum reports whether c is an ASCII letter or digit.
func isAlnum(c byte) bool {
	return 'a' <= c && c <= 'z' || 'A' <= c && c <= 'Z' || '0' <= c && c <= '9'
}
