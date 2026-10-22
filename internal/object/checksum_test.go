package object

import "testing"

func TestChecksumBase64(t *testing.T) {
	c := Checksum{Algorithm: ChecksumCRC32, Value: []byte{0, 0, 0, 1}}
	if c.Base64() != "AAAAAQ==" {
		t.Fatal(c.Base64())
	}
	c.Type, c.Parts = ChecksumComposite, 3
	if c.Base64() != "AAAAAQ==-3" {
		t.Fatal(c.Base64())
	}
}

func TestParseChecksumAlgorithm(t *testing.T) {
	if a, ok := ParseChecksumAlgorithm("CRC64NVME"); !ok || a != ChecksumCRC64NVME || a.String() != "CRC64NVME" {
		t.Fatal("parse CRC64NVME")
	}
	if _, ok := ParseChecksumAlgorithm("crc32"); ok {
		t.Fatal("names are case-sensitive")
	}
	if ChecksumNone.String() != "" {
		t.Fatal("none must be empty")
	}
}
