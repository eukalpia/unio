package integration

import (
	"bytes"
	"crypto/sha1"
	"crypto/sha256"
	"encoding/base64"
	"encoding/binary"
	"hash/crc32"
	"hash/crc64"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/aws-sdk-go-v2/service/s3/types"
)

var (
	crc32cTable = crc32.MakeTable(crc32.Castagnoli)

	// nvmeTable implements CRC-64/NVME. hash/crc64 takes the reflected form of
	// the polynomial, and the reflection of the normal form 0xAD93D23594C93659
	// is 0x9A6C9329AC4BC9B5.
	nvmeTable = crc64.MakeTable(0x9a6c9329ac4bc9b5)
)

// base64Of encodes a digest the way x-amz-checksum-* headers carry it.
func base64Of(digest []byte) string {
	return base64.StdEncoding.EncodeToString(digest)
}

func crc32Base64(data []byte) string {
	var b [4]byte
	binary.BigEndian.PutUint32(b[:], crc32.ChecksumIEEE(data))
	return base64Of(b[:])
}

func crc32cBase64(data []byte) string {
	var b [4]byte
	binary.BigEndian.PutUint32(b[:], crc32.Checksum(data, crc32cTable))
	return base64Of(b[:])
}

func crc64nvmeBase64(data []byte) string {
	var b [8]byte
	binary.BigEndian.PutUint64(b[:], crc64.Checksum(data, nvmeTable))
	return base64Of(b[:])
}

func sha1Base64(data []byte) string {
	sum := sha1.Sum(data)
	return base64Of(sum[:])
}

func sha256Base64(data []byte) string {
	sum := sha256.Sum256(data)
	return base64Of(sum[:])
}

// checksumSet holds the x-amz-checksum-* values of one object, keyed by
// algorithm. Values are base64 digests, as S3 returns them.
type checksumSet struct {
	crc32, crc32c, crc64nvme, sha1, sha256 string
}

func checksumsFromPut(out *s3.PutObjectOutput) checksumSet {
	return checksumSet{
		crc32:     aws.ToString(out.ChecksumCRC32),
		crc32c:    aws.ToString(out.ChecksumCRC32C),
		crc64nvme: aws.ToString(out.ChecksumCRC64NVME),
		sha1:      aws.ToString(out.ChecksumSHA1),
		sha256:    aws.ToString(out.ChecksumSHA256),
	}
}

func checksumsFromGet(out *s3.GetObjectOutput) checksumSet {
	return checksumSet{
		crc32:     aws.ToString(out.ChecksumCRC32),
		crc32c:    aws.ToString(out.ChecksumCRC32C),
		crc64nvme: aws.ToString(out.ChecksumCRC64NVME),
		sha1:      aws.ToString(out.ChecksumSHA1),
		sha256:    aws.ToString(out.ChecksumSHA256),
	}
}

func checksumsFromHead(out *s3.HeadObjectOutput) checksumSet {
	return checksumSet{
		crc32:     aws.ToString(out.ChecksumCRC32),
		crc32c:    aws.ToString(out.ChecksumCRC32C),
		crc64nvme: aws.ToString(out.ChecksumCRC64NVME),
		sha1:      aws.ToString(out.ChecksumSHA1),
		sha256:    aws.ToString(out.ChecksumSHA256),
	}
}

// checksumAlgorithm describes one checksum algorithm as the suite exercises it.
type checksumAlgorithm struct {
	name    string
	alg     types.ChecksumAlgorithm
	compute func(data []byte) string                  // base64 digest of data
	pick    func(cs checksumSet) string               // this algorithm's value in cs
	set     func(in *s3.PutObjectInput, value string) // the matching x-amz-checksum-* input field
}

// checksumAlgorithms covers the algorithms of architecture section 9.
var checksumAlgorithms = []checksumAlgorithm{
	{
		name:    "CRC32",
		alg:     types.ChecksumAlgorithmCrc32,
		compute: crc32Base64,
		pick:    func(cs checksumSet) string { return cs.crc32 },
		set:     func(in *s3.PutObjectInput, v string) { in.ChecksumCRC32 = aws.String(v) },
	},
	{
		name:    "CRC32C",
		alg:     types.ChecksumAlgorithmCrc32c,
		compute: crc32cBase64,
		pick:    func(cs checksumSet) string { return cs.crc32c },
		set:     func(in *s3.PutObjectInput, v string) { in.ChecksumCRC32C = aws.String(v) },
	},
	{
		name:    "CRC64NVME",
		alg:     types.ChecksumAlgorithmCrc64nvme,
		compute: crc64nvmeBase64,
		pick:    func(cs checksumSet) string { return cs.crc64nvme },
		set:     func(in *s3.PutObjectInput, v string) { in.ChecksumCRC64NVME = aws.String(v) },
	},
	{
		name:    "SHA1",
		alg:     types.ChecksumAlgorithmSha1,
		compute: sha1Base64,
		pick:    func(cs checksumSet) string { return cs.sha1 },
		set:     func(in *s3.PutObjectInput, v string) { in.ChecksumSHA1 = aws.String(v) },
	},
	{
		name:    "SHA256",
		alg:     types.ChecksumAlgorithmSha256,
		compute: sha256Base64,
		pick:    func(cs checksumSet) string { return cs.sha256 },
		set:     func(in *s3.PutObjectInput, v string) { in.ChecksumSHA256 = aws.String(v) },
	},
}

func TestChecksumAlgorithms(t *testing.T) {
	for _, alg := range checksumAlgorithms {
		t.Run(alg.name, func(t *testing.T) {
			t.Parallel()
			ctx, cli := setup(t)
			bucket := newBucket(ctx, t, cli)
			key := "checksum/" + strings.ToLower(alg.name)
			body := randomBytes(t, 256<<10)
			want := alg.compute(body)

			put, err := cli.PutObject(ctx, &s3.PutObjectInput{
				Bucket:            aws.String(bucket),
				Key:               aws.String(key),
				Body:              bytes.NewReader(body),
				ChecksumAlgorithm: alg.alg,
			})
			if err != nil {
				t.Fatalf("PutObject with %s: %v", alg.name, err)
			}
			if got := alg.pick(checksumsFromPut(put)); got != want {
				t.Errorf("PutObject %s = %q, want %q", alg.name, got, want)
			}

			head, err := cli.HeadObject(ctx, &s3.HeadObjectInput{
				Bucket:       aws.String(bucket),
				Key:          aws.String(key),
				ChecksumMode: types.ChecksumModeEnabled,
			})
			if err != nil {
				t.Fatalf("HeadObject: %v", err)
			}
			if got := alg.pick(checksumsFromHead(head)); got != want {
				t.Errorf("HeadObject %s = %q, want %q", alg.name, got, want)
			}

			get, err := cli.GetObject(ctx, &s3.GetObjectInput{
				Bucket:       aws.String(bucket),
				Key:          aws.String(key),
				ChecksumMode: types.ChecksumModeEnabled,
			})
			if err != nil {
				t.Fatalf("GetObject: %v", err)
			}
			if got := alg.pick(checksumsFromGet(get)); got != want {
				t.Errorf("GetObject %s = %q, want %q", alg.name, got, want)
			}
			if got := bodyOf(t, get); sha256Hex(got) != sha256Hex(body) {
				t.Errorf("GetObject body SHA-256 mismatch")
			}
		})
	}
}

func TestChecksumMismatchRejected(t *testing.T) {
	for _, alg := range checksumAlgorithms {
		t.Run(alg.name, func(t *testing.T) {
			t.Parallel()
			ctx, cli := setup(t)
			bucket := newBucket(ctx, t, cli)
			key := "bad-checksum/" + strings.ToLower(alg.name)
			body := randomBytes(t, 64<<10)

			// The value is for different bytes, so the server must reject the body.
			in := &s3.PutObjectInput{Bucket: aws.String(bucket), Key: aws.String(key), Body: bytes.NewReader(body)}
			alg.set(in, alg.compute([]byte("different bytes")))
			_, err := cli.PutObject(ctx, in)
			requireAPIError(t, err, http.StatusBadRequest, "BadDigest")

			_, err = cli.HeadObject(ctx, &s3.HeadObjectInput{Bucket: aws.String(bucket), Key: aws.String(key)})
			requireAPIError(t, err, http.StatusNotFound, "NotFound")
		})
	}
}

// streamOnly hides the Seek method of its reader, so the SDK cannot rewind the
// body. It must stream the body, and with the default settings it sends the
// checksum as a trailer.
type streamOnly struct {
	r io.Reader
}

// Read reads from the wrapped reader.
func (s streamOnly) Read(p []byte) (int, error) {
	return s.r.Read(p)
}

func TestChecksumDefaultTrailerStreamingUpload(t *testing.T) {
	t.Parallel()
	// newClient leaves RequestChecksumCalculation unset, so the SDK default
	// (WhenSupported) applies. That default is resolved per operation, which is
	// why the client options cannot show it.
	ctx, cli := setup(t)
	if !strings.HasPrefix(aws.ToString(cli.Options().BaseEndpoint), "https://") {
		// The SDK sends an unseekable body with a trailing checksum only over
		// TLS. Over plain HTTP it refuses the body before sending anything.
		t.Skip("trailing-checksum streaming needs an https endpoint; set UNIO_TEST_ENDPOINT to https:// (see README)")
	}
	bucket := newBucket(ctx, t, cli)
	const key = "checksum/trailer"
	body := randomBytes(t, 1<<20+17)

	_, err := cli.PutObject(ctx, &s3.PutObjectInput{
		Bucket:        aws.String(bucket),
		Key:           aws.String(key),
		Body:          streamOnly{r: bytes.NewReader(body)},
		ContentLength: aws.Int64(int64(len(body))),
	})
	if err != nil {
		t.Fatalf("streaming PutObject: %v", err)
	}
	get, err := cli.GetObject(ctx, &s3.GetObjectInput{
		Bucket:       aws.String(bucket),
		Key:          aws.String(key),
		ChecksumMode: types.ChecksumModeEnabled,
	})
	if err != nil {
		t.Fatalf("GetObject: %v", err)
	}
	if got := checksumsFromGet(get).crc32; got != crc32Base64(body) {
		t.Errorf("stored CRC32 = %q, want %q", got, crc32Base64(body))
	}
	if got := bodyOf(t, get); sha256Hex(got) != sha256Hex(body) {
		t.Errorf("GetObject body SHA-256 mismatch")
	}
}

func TestChecksumKnownAnswers(t *testing.T) {
	t.Parallel()
	// Check values for the ASCII string "123456789" from the CRC catalogue.
	check := []byte("123456789")
	tests := []struct {
		name string
		got  uint64
		want uint64
	}{
		{name: "CRC-32", got: uint64(crc32.ChecksumIEEE(check)), want: 0xCBF43926},
		{name: "CRC-32C", got: uint64(crc32.Checksum(check, crc32cTable)), want: 0xE3069283},
		{name: "CRC-64/NVME", got: crc64.Checksum(check, nvmeTable), want: 0xAE8B14860A799888},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if tc.got != tc.want {
				t.Fatalf("checksum = %#x, want %#x", tc.got, tc.want)
			}
		})
	}
}
