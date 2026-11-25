package admin

import (
	"strings"
	"unicode"
)

// maxLoggedValue caps one user-supplied value in a log entry.
const maxLoggedValue = 256

// logSafe prepares a user-supplied value for a log attribute. Line breaks are
// removed first, so a value can never start a forged log line in the text
// format. Other control characters go too, and the value is capped at
// maxLoggedValue bytes. The JSON handler escapes all of this already; the text
// handler and log readers that do not parse JSON need the guarantee as well.
func logSafe(value string) string {
	value = strings.ReplaceAll(value, "\n", "")
	value = strings.ReplaceAll(value, "\r", "")
	value = strings.Map(func(r rune) rune {
		if unicode.IsControl(r) {
			return -1
		}
		return r
	}, value)
	if len(value) > maxLoggedValue {
		value = value[:maxLoggedValue]
	}
	return value
}
