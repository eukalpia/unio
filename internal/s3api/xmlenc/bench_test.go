package xmlenc

import (
	"encoding/xml"
	"fmt"
	"testing"
	"time"
)

// benchContents returns n listing entries with typical S3 key lengths.
func benchContents(n int) []Content {
	cs := make([]Content, n)
	for i := range cs {
		cs[i] = Content{
			Key:          fmt.Sprintf("logs/2026/10/service-a/instance-%04d/part-%08d.parquet", i%64, i),
			ETag:         `"9e107d9d372bb6826bd81d3542a419d6"`,
			StorageClass: "STANDARD",
			LastModified: testTime.Add(time.Duration(i) * time.Second),
			Size:         int64(4096 + i*17),
		}
	}
	return cs
}

// benchListResult is the shared 1000-entry listing used by both benchmarks.
func benchListResult() ListObjectsV2Result {
	return ListObjectsV2Result{
		Name:                  "bench-bucket",
		Prefix:                "logs/2026/10/",
		NextContinuationToken: "1ueGcxLPRx1Tr/YsbH1kyr7ZTYa8QnXUUr3EYWI9B=",
		KeyCount:              1000,
		MaxKeys:               1000,
		IsTruncated:           true,
		Contents:              benchContents(1000),
	}
}

// xmlContent mirrors Content for the encoding/xml baseline.
type xmlContent struct {
	Key          string    `xml:"Key"`
	LastModified time.Time `xml:"LastModified"`
	ETag         string    `xml:"ETag"`
	Size         int64     `xml:"Size"`
	StorageClass string    `xml:"StorageClass"`
}

// xmlListBucketResult mirrors ListObjectsV2Result for the encoding/xml baseline.
type xmlListBucketResult struct {
	XMLName               xml.Name     `xml:"ListBucketResult"`
	Name                  string       `xml:"Name"`
	Prefix                string       `xml:"Prefix"`
	NextContinuationToken string       `xml:"NextContinuationToken"`
	KeyCount              int          `xml:"KeyCount"`
	MaxKeys               int          `xml:"MaxKeys"`
	IsTruncated           bool         `xml:"IsTruncated"`
	Contents              []xmlContent `xml:"Contents"`
}

func BenchmarkAppendListObjectsV2(b *testing.B) {
	r := benchListResult()
	dst := AppendListObjectsV2(nil, &r) // warm-up grows dst once
	b.SetBytes(int64(len(dst)))
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		dst = AppendListObjectsV2(dst[:0], &r)
	}
}

func BenchmarkEncodingXMLListObjectsV2(b *testing.B) {
	r := benchListResult()
	doc := xmlListBucketResult{
		Name:                  r.Name,
		Prefix:                r.Prefix,
		NextContinuationToken: r.NextContinuationToken,
		KeyCount:              r.KeyCount,
		MaxKeys:               r.MaxKeys,
		IsTruncated:           r.IsTruncated,
		Contents:              make([]xmlContent, len(r.Contents)),
	}
	for i, c := range r.Contents {
		doc.Contents[i] = xmlContent{
			Key: c.Key, LastModified: c.LastModified, ETag: c.ETag, Size: c.Size, StorageClass: c.StorageClass,
		}
	}
	out, err := xml.Marshal(&doc)
	if err != nil {
		b.Fatal(err)
	}
	b.SetBytes(int64(len(out)))
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := xml.Marshal(&doc); err != nil {
			b.Fatal(err)
		}
	}
}

// TestAppendNoAllocsAfterWarmup checks that the append path allocates nothing
// once the destination has grown to its final size.
func TestAppendNoAllocsAfterWarmup(t *testing.T) {
	r := benchListResult()
	dst := AppendListObjectsV2(nil, &r)
	if allocs := testing.AllocsPerRun(20, func() { dst = AppendListObjectsV2(dst[:0], &r) }); allocs != 0 {
		t.Errorf("AppendListObjectsV2 allocated %.1f times per call, want 0", allocs)
	}

	esc := make([]byte, 0, 256)
	const s = "key with &<>\"' and \r and emoji 😀"
	if allocs := testing.AllocsPerRun(100, func() { esc = AppendEscape(esc[:0], s) }); allocs != 0 {
		t.Errorf("AppendEscape allocated %.1f times per call, want 0", allocs)
	}
}
