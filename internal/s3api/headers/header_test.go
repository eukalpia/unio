package headers

import "testing"

func TestHasPrefixFold(t *testing.T) {
	cases := []struct {
		s, prefix string
		want      bool
	}{
		{"X-Amz-Meta-Foo", "x-amz-meta-", true},
		{"x-amz-meta-", "x-amz-meta-", true},
		{"X-AMZ-META-", "x-amz-meta-", true},
		{"X-Amz-Metadata-Directive", "x-amz-meta-", false},
		{"X-Amz-Met", "x-amz-meta-", false},
		{"", "x-amz-meta-", false},
		{"X-Amz-Checksum-Crc32", "x-amz-checksum-", true},
		{"X-Amz-Checksum-", "x-amz-checksum-", true},
		{"Content-Type", "x-amz-checksum-", false},
	}
	for _, c := range cases {
		if got := hasPrefixFold(c.s, c.prefix); got != c.want {
			t.Fatalf("hasPrefixFold(%q, %q) = %v, want %v", c.s, c.prefix, got, c.want)
		}
	}
}

func TestParseDigits(t *testing.T) {
	cases := []struct {
		in   string
		want int64
		ok   bool
	}{
		{"0", 0, true},
		{"007", 7, true},
		{"9223372036854775807", 9223372036854775807, true},
		{"", 0, false},
		{"+1", 0, false},
		{"-1", 0, false},
		{" 1", 0, false},
		{"1 ", 0, false},
		{"0x1", 0, false},
		{"9223372036854775808", 0, false},
	}
	for _, c := range cases {
		got, ok := parseDigits(c.in)
		if ok != c.ok || (ok && got != c.want) {
			t.Fatalf("parseDigits(%q) = %d, %v; want %d, %v", c.in, got, ok, c.want, c.ok)
		}
	}
}
