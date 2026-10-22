package headers

import (
	"net/http"
	"testing"
)

func BenchmarkParseRange(b *testing.B) {
	const v = "bytes=0-1048575"
	b.ReportAllocs()
	for range b.N {
		if _, err := ParseRange(v); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkParseObjectAttributes(b *testing.B) {
	h := http.Header{
		"Content-Type":                 {"application/octet-stream"},
		"Cache-Control":                {"max-age=3600"},
		"X-Amz-Meta-Owner":             {"alice"},
		"X-Amz-Meta-Project":           {"unio"},
		"X-Amz-Tagging":                {"env=prod&team=storage"},
		"X-Amz-Storage-Class":          {"STANDARD"},
		"X-Amz-Sdk-Checksum-Algorithm": {"CRC32"},
		"X-Amz-Checksum-Crc32":         {b64(0, 0, 0, 1)},
		"If-None-Match":                {"*"},
	}
	b.ReportAllocs()
	for range b.N {
		if _, err := ParseObjectAttributes(h); err != nil {
			b.Fatal(err)
		}
	}
}
