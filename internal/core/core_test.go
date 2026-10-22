package core

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"
)

func TestHTTPRangeResolve(t *testing.T) {
	cases := []struct {
		r           HTTPRange
		size        int64
		off, length int64
		wantErr     bool
	}{
		{HTTPRange{Start: 0, End: 9}, 100, 0, 10, false},
		{HTTPRange{Start: 90, End: -1}, 100, 90, 10, false},
		{HTTPRange{Start: 90, End: 1000}, 100, 90, 10, false},
		{HTTPRange{Suffix: 10}, 100, 90, 10, false},
		{HTTPRange{Suffix: 500}, 100, 0, 100, false},
		{HTTPRange{Start: 100, End: -1}, 100, 0, 0, true},
		{HTTPRange{Start: 5, End: 2}, 100, 0, 0, true},
		{HTTPRange{Suffix: 1}, 0, 0, 0, true},
	}
	for _, c := range cases {
		off, n, err := c.r.Resolve(c.size)
		if (err != nil) != c.wantErr {
			t.Fatalf("%+v size %d: err = %v, wantErr %v", c.r, c.size, err, c.wantErr)
		}
		if err != nil {
			if !IsCode(err, ErrInvalidRange) {
				t.Fatalf("want InvalidRange, got %v", err)
			}
			continue
		}
		if off != c.off || n != c.length {
			t.Fatalf("%+v size %d: got (%d,%d) want (%d,%d)", c.r, c.size, off, n, c.off, c.length)
		}
	}
}

func TestConditionsEvaluateRead(t *testing.T) {
	mod := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	info := ObjectInfo{ETag: "abc", ModTime: mod}
	cases := []struct {
		c    Conditions
		want ErrorCode
	}{
		{Conditions{}, ""},
		{Conditions{IfMatch: "abc"}, ""},
		{Conditions{IfMatch: "zzz"}, ErrPreconditionFailed},
		{Conditions{IfNoneMatch: "abc"}, ErrNotModified},
		{Conditions{IfNoneMatch: "zzz"}, ""},
		{Conditions{IfModifiedSince: mod}, ErrNotModified},
		{Conditions{IfModifiedSince: mod.Add(-time.Hour)}, ""},
		{Conditions{IfUnmodifiedSince: mod.Add(-time.Hour)}, ErrPreconditionFailed},
		// If-Match takes precedence over If-Unmodified-Since.
		{Conditions{IfMatch: "abc", IfUnmodifiedSince: mod.Add(-time.Hour)}, ""},
		// If-None-Match takes precedence over If-Modified-Since.
		{Conditions{IfNoneMatch: "zzz", IfModifiedSince: mod}, ""},
	}
	for i, c := range cases {
		if got := CodeOf(c.c.EvaluateRead(info)); got != c.want {
			t.Fatalf("case %d: got %q want %q", i, got, c.want)
		}
	}
}

func TestConditionsEvaluateWrite(t *testing.T) {
	cases := []struct {
		c      Conditions
		exists bool
		want   ErrorCode
	}{
		{Conditions{}, false, ""},
		{Conditions{IfNoneMatch: "*"}, false, ""},
		{Conditions{IfNoneMatch: "*"}, true, ErrPreconditionFailed},
		{Conditions{IfMatch: "abc"}, true, ""},
		{Conditions{IfMatch: "zzz"}, true, ErrPreconditionFailed},
		{Conditions{IfMatch: "abc"}, false, ErrNoSuchKey},
	}
	for i, c := range cases {
		if got := CodeOf(c.c.EvaluateWrite(c.exists, "abc")); got != c.want {
			t.Fatalf("case %d: got %q want %q", i, got, c.want)
		}
	}
}

func TestCodeOf(t *testing.T) {
	cases := []struct {
		err  error
		want ErrorCode
	}{
		{nil, ""},
		{Errorf(ErrNoSuchKey, "x"), ErrNoSuchKey},
		{fmt.Errorf("wrapped: %w", Errorf(ErrAccessDenied, "no")), ErrAccessDenied},
		{fmt.Errorf("w: %w", ErrWriteQuorum), ErrServiceUnavailable},
		{ErrReadQuorum, ErrServiceUnavailable},
		{context.DeadlineExceeded, ErrRequestTimeout},
		{errors.New("boom"), ErrInternalError},
	}
	for i, c := range cases {
		if got := CodeOf(c.err); got != c.want {
			t.Fatalf("case %d: got %q want %q", i, got, c.want)
		}
	}
	if !errors.Is(fmt.Errorf("w: %w", Errorf(ErrNoSuchKey, "a")), Errorf(ErrNoSuchKey, "b")) {
		t.Fatal("errors.Is should match by code")
	}
	if ErrNoSuchKey.HTTPStatus() != http.StatusNotFound || ErrorCode("Unknown").HTTPStatus() != 500 {
		t.Fatal("bad status mapping")
	}
	// Internal causes must not leak into client messages.
	e := Wrap(ErrInternalError, errors.New("disk /dev/sda exploded"))
	if e.Message != "" {
		t.Fatal("Wrap must not set a client message")
	}
}

func TestValidateBucketName(t *testing.T) {
	good := []string{"abc", "my-bucket", "my.bucket.1", strings.Repeat("a", 63)}
	bad := []string{"ab", strings.Repeat("a", 64), "Abc", "-abc", "abc-", "a..b", "a.-b", "192.168.1.1", "xn--abc", "abc-s3alias", "a_b"}
	for _, n := range good {
		if err := ValidateBucketName(n); err != nil {
			t.Fatalf("%q: unexpected %v", n, err)
		}
	}
	for _, n := range bad {
		if err := ValidateBucketName(n); !IsCode(err, ErrInvalidBucketName) {
			t.Fatalf("%q: want InvalidBucketName, got %v", n, err)
		}
	}
}

func TestValidateObjectKey(t *testing.T) {
	if err := ValidateObjectKey("a/b/c.txt"); err != nil {
		t.Fatal(err)
	}
	if !IsCode(ValidateObjectKey(strings.Repeat("k", MaxObjectKeyLength+1)), ErrKeyTooLongError) {
		t.Fatal("want KeyTooLongError")
	}
	for _, k := range []string{"", "a\x00b", string([]byte{0xff, 0xfe})} {
		if !IsCode(ValidateObjectKey(k), ErrInvalidArgument) {
			t.Fatalf("%q: want InvalidArgument", k)
		}
	}
}

func TestShardIDBinary(t *testing.T) {
	s := ShardID{Data: NewDataID(), Index: 513}
	b := s.AppendBinary(nil)
	if len(b) != ShardIDSize {
		t.Fatalf("len %d", len(b))
	}
	got, err := ParseShardIDBinary(b)
	if err != nil || got != s {
		t.Fatalf("got %v %v", got, err)
	}
	if _, err := ParseShardIDBinary(b[:5]); err == nil {
		t.Fatal("expected error")
	}
	id, err := ParseDataID(s.Data.String())
	if err != nil || id != s.Data {
		t.Fatal("data id hex round trip failed")
	}
}

func TestPartitionOf(t *testing.T) {
	counts := make([]int, 17)
	for i := 0; i < 16000; i++ {
		p := PartitionOf(7, fmt.Sprintf("key-%d", i), 16)
		if p < 1 || p > 16 {
			t.Fatalf("partition %d out of range", p)
		}
		counts[p]++
	}
	for p := 1; p <= 16; p++ {
		if counts[p] < 700 || counts[p] > 1300 {
			t.Fatalf("partition %d has %d keys: poor distribution", p, counts[p])
		}
	}
	if a, b := PartitionOf(7, "a", 16), PartitionOf(7, "a", 16); a != b {
		t.Fatal("not deterministic")
	}
	if PartitionOf(1, "a", 0) != SystemPartition {
		t.Fatal("zero partitions must map to system")
	}
}

func TestErasureLayoutQuorum(t *testing.T) {
	if (ErasureLayout{DataShards: 4, ParityShards: 2}).WriteQuorum() != 4 {
		t.Fatal("4+2")
	}
	if (ErasureLayout{DataShards: 2, ParityShards: 2}).WriteQuorum() != 3 {
		t.Fatal("2+2")
	}
}

func TestChecksumBase64(t *testing.T) {
	c := Checksum{Algorithm: ChecksumCRC32, Value: []byte{0, 0, 0, 1}}
	if c.Base64() != "AAAAAQ==" {
		t.Fatal(c.Base64())
	}
	c.Type, c.Parts = ChecksumComposite, 3
	if c.Base64() != "AAAAAQ==-3" {
		t.Fatal(c.Base64())
	}
	if a, ok := ParseChecksumAlgorithm("CRC64NVME"); !ok || a != ChecksumCRC64NVME || a.String() != "CRC64NVME" {
		t.Fatal("parse CRC64NVME")
	}
	if _, ok := ParseChecksumAlgorithm("crc32"); ok {
		t.Fatal("names are case-sensitive")
	}
}
