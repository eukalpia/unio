package xmlenc

import (
	"encoding/xml"
	"net/url"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"
)

// Mirror types decode the documents produced by this package with encoding/xml.

type rtOwner struct {
	ID          string `xml:"ID"`
	DisplayName string `xml:"DisplayName"`
}

type rtPrefix struct {
	Prefix string `xml:"Prefix"`
}

type rtContents struct {
	Key               string    `xml:"Key"`
	LastModified      time.Time `xml:"LastModified"`
	ETag              string    `xml:"ETag"`
	ChecksumAlgorithm string    `xml:"ChecksumAlgorithm"`
	Size              int64     `xml:"Size"`
	StorageClass      string    `xml:"StorageClass"`
	Owner             *rtOwner  `xml:"Owner"`
}

type rtListV2 struct {
	XMLName               xml.Name     `xml:"ListBucketResult"`
	Name                  string       `xml:"Name"`
	Prefix                string       `xml:"Prefix"`
	Delimiter             string       `xml:"Delimiter"`
	StartAfter            string       `xml:"StartAfter"`
	ContinuationToken     string       `xml:"ContinuationToken"`
	NextContinuationToken string       `xml:"NextContinuationToken"`
	EncodingType          string       `xml:"EncodingType"`
	KeyCount              int          `xml:"KeyCount"`
	MaxKeys               int          `xml:"MaxKeys"`
	IsTruncated           bool         `xml:"IsTruncated"`
	Contents              []rtContents `xml:"Contents"`
	CommonPrefixes        []rtPrefix   `xml:"CommonPrefixes"`
}

type rtDeleted struct {
	Key                   string `xml:"Key"`
	VersionID             string `xml:"VersionId"`
	DeleteMarker          bool   `xml:"DeleteMarker"`
	DeleteMarkerVersionID string `xml:"DeleteMarkerVersionId"`
}

type rtDeleteError struct {
	Key       string `xml:"Key"`
	VersionID string `xml:"VersionId"`
	Code      string `xml:"Code"`
	Message   string `xml:"Message"`
}

type rtDeleteResult struct {
	XMLName xml.Name        `xml:"DeleteResult"`
	Deleted []rtDeleted     `xml:"Deleted"`
	Errors  []rtDeleteError `xml:"Error"`
}

type rtPart struct {
	PartNumber    int       `xml:"PartNumber"`
	LastModified  time.Time `xml:"LastModified"`
	ETag          string    `xml:"ETag"`
	Size          int64     `xml:"Size"`
	ChecksumCRC32 string    `xml:"ChecksumCRC32"`
}

type rtListParts struct {
	XMLName              xml.Name `xml:"ListPartsResult"`
	Bucket               string   `xml:"Bucket"`
	Key                  string   `xml:"Key"`
	UploadID             string   `xml:"UploadId"`
	Initiator            *rtOwner `xml:"Initiator"`
	Owner                *rtOwner `xml:"Owner"`
	StorageClass         string   `xml:"StorageClass"`
	PartNumberMarker     int      `xml:"PartNumberMarker"`
	NextPartNumberMarker int      `xml:"NextPartNumberMarker"`
	MaxParts             int      `xml:"MaxParts"`
	IsTruncated          bool     `xml:"IsTruncated"`
	Parts                []rtPart `xml:"Part"`
	ChecksumAlgorithm    string   `xml:"ChecksumAlgorithm"`
}

type rtUpload struct {
	Key          string    `xml:"Key"`
	UploadID     string    `xml:"UploadId"`
	Initiator    *rtOwner  `xml:"Initiator"`
	Owner        *rtOwner  `xml:"Owner"`
	StorageClass string    `xml:"StorageClass"`
	Initiated    time.Time `xml:"Initiated"`
}

type rtListUploads struct {
	XMLName            xml.Name   `xml:"ListMultipartUploadsResult"`
	Bucket             string     `xml:"Bucket"`
	KeyMarker          string     `xml:"KeyMarker"`
	UploadIDMarker     string     `xml:"UploadIdMarker"`
	NextKeyMarker      string     `xml:"NextKeyMarker"`
	Prefix             string     `xml:"Prefix"`
	Delimiter          string     `xml:"Delimiter"`
	NextUploadIDMarker string     `xml:"NextUploadIdMarker"`
	EncodingType       string     `xml:"EncodingType"`
	MaxUploads         int        `xml:"MaxUploads"`
	IsTruncated        bool       `xml:"IsTruncated"`
	Uploads            []rtUpload `xml:"Upload"`
	CommonPrefixes     []rtPrefix `xml:"CommonPrefixes"`
}

type rtError struct {
	XMLName   xml.Name `xml:"Error"`
	Code      string   `xml:"Code"`
	Message   string   `xml:"Message"`
	Resource  string   `xml:"Resource"`
	RequestID string   `xml:"RequestId"`
	HostID    string   `xml:"HostId"`
}

// awkwardKeys holds values that stress the XML escaping rules.
func awkwardKeys() []string {
	return []string{"a&b", "<tag>", "]]>", "😀 emoji", "cr\rlf\n", "quote\"apos'", "tab\there"}
}

func TestRoundTripListObjectsV2(t *testing.T) {
	keys := awkwardKeys()
	r := ListObjectsV2Result{
		Name:                  "bucket&<>",
		Prefix:                "]]>/",
		Delimiter:             "\r",
		StartAfter:            "a&b",
		ContinuationToken:     "t<1>",
		NextContinuationToken: "t\"2\"'",
		KeyCount:              len(keys),
		MaxKeys:               1000,
		IsTruncated:           true,
	}
	want := rtListV2{
		Name:                  r.Name,
		Prefix:                r.Prefix,
		Delimiter:             r.Delimiter,
		StartAfter:            r.StartAfter,
		ContinuationToken:     r.ContinuationToken,
		NextContinuationToken: r.NextContinuationToken,
		KeyCount:              r.KeyCount,
		MaxKeys:               r.MaxKeys,
		IsTruncated:           true,
	}
	for _, k := range keys {
		r.Contents = append(r.Contents, Content{
			Key: k, ETag: `"e&"`, StorageClass: "STANDARD", LastModified: testTime, Size: 3,
			Owner: &Owner{ID: k, DisplayName: "<n>"},
		})
		want.Contents = append(want.Contents, rtContents{
			Key: k, ETag: `"e&"`, StorageClass: "STANDARD", LastModified: testTime, Size: 3,
			Owner: &rtOwner{ID: k, DisplayName: "<n>"},
		})
		r.CommonPrefixes = append(r.CommonPrefixes, k)
		want.CommonPrefixes = append(want.CommonPrefixes, rtPrefix{Prefix: k})
	}

	var got rtListV2
	if err := xml.Unmarshal(AppendListObjectsV2(nil, &r), &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if diff := cmp.Diff(want, got, cmpopts.IgnoreFields(rtListV2{}, "XMLName")); diff != "" {
		t.Errorf("round trip mismatch (-want +got):\n%s", diff)
	}
	if got.XMLName.Local != "ListBucketResult" {
		t.Errorf("root element = %q, want ListBucketResult", got.XMLName.Local)
	}
}

func TestRoundTripListObjectsV2URLEncoding(t *testing.T) {
	// Expected values are the percent-encoded forms written out by hand, so
	// the test does not depend on AppendPercent to check itself.
	tests := []struct {
		key     string
		encoded string
	}{
		{key: "a&b", encoded: "a%26b"},
		{key: "<tag>", encoded: "%3Ctag%3E"},
		{key: "]]>", encoded: "%5D%5D%3E"},
		{key: "😀 emoji", encoded: "%F0%9F%98%80%20emoji"},
		{key: "cr\rlf\n", encoded: "cr%0Dlf%0A"},
		{key: "quote\"apos'", encoded: "quote%22apos%27"},
		{key: "tab\there", encoded: "tab%09here"},
	}
	for _, tc := range tests {
		t.Run(tc.encoded, func(t *testing.T) {
			r := ListObjectsV2Result{
				Name:         "b",
				EncodingType: "url",
				MaxKeys:      1,
				Contents:     []Content{{Key: tc.key, LastModified: testTime}},
			}
			var got rtListV2
			if err := xml.Unmarshal(AppendListObjectsV2(nil, &r), &got); err != nil {
				t.Fatalf("unmarshal: %v", err)
			}
			if len(got.Contents) != 1 {
				t.Fatalf("got %d contents, want 1", len(got.Contents))
			}
			if key := got.Contents[0].Key; key != tc.encoded {
				t.Errorf("decoded key = %q, want %q", key, tc.encoded)
			}
			back, err := url.PathUnescape(got.Contents[0].Key)
			if err != nil || back != tc.key {
				t.Errorf("PathUnescape = %q, %v; want %q", back, err, tc.key)
			}
		})
	}
}

func TestRoundTripDeleteResult(t *testing.T) {
	const message = "no <access> & \r"
	r := DeleteResult{}
	want := rtDeleteResult{}
	for _, k := range awkwardKeys() {
		r.Deleted = append(r.Deleted, DeletedObject{Key: k, VersionID: "v&1"})
		want.Deleted = append(want.Deleted, rtDeleted{Key: k, VersionID: "v&1"})
		r.Errors = append(r.Errors, DeleteError{Key: k, Code: "AccessDenied", Message: message})
		want.Errors = append(want.Errors, rtDeleteError{Key: k, Code: "AccessDenied", Message: message})
	}
	r.Deleted = append(r.Deleted, DeletedObject{Key: "m\r", DeleteMarker: true, DeleteMarkerVersionID: "dm&1"})
	want.Deleted = append(want.Deleted, rtDeleted{Key: "m\r", DeleteMarker: true, DeleteMarkerVersionID: "dm&1"})

	var got rtDeleteResult
	if err := xml.Unmarshal(AppendDeleteResult(nil, &r), &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if diff := cmp.Diff(want, got, cmpopts.IgnoreFields(rtDeleteResult{}, "XMLName")); diff != "" {
		t.Errorf("round trip mismatch (-want +got):\n%s", diff)
	}
}

func TestRoundTripListParts(t *testing.T) {
	owner := &Owner{ID: "o&", DisplayName: "d\"'"}
	r := ListPartsResult{
		Bucket:               "b&",
		Key:                  "k<\r>",
		UploadID:             "u]]>",
		StorageClass:         "STANDARD",
		ChecksumAlgorithm:    "CRC32",
		Owner:                owner,
		PartNumberMarker:     1,
		NextPartNumberMarker: 3,
		MaxParts:             2,
		IsTruncated:          true,
		Parts: []Part{{
			PartNumber: 2, LastModified: testTime, ETag: `"p&"`, Size: 9,
			Checksums: Checksums{CRC32: "AQID"},
		}},
	}
	want := rtListParts{
		Bucket:               "b&",
		Key:                  "k<\r>",
		UploadID:             "u]]>",
		Initiator:            &rtOwner{ID: "o&", DisplayName: "d\"'"},
		Owner:                &rtOwner{ID: "o&", DisplayName: "d\"'"},
		StorageClass:         "STANDARD",
		PartNumberMarker:     1,
		NextPartNumberMarker: 3,
		MaxParts:             2,
		IsTruncated:          true,
		Parts: []rtPart{{
			PartNumber: 2, LastModified: testTime, ETag: `"p&"`, Size: 9, ChecksumCRC32: "AQID",
		}},
		ChecksumAlgorithm: "CRC32",
	}

	var got rtListParts
	if err := xml.Unmarshal(AppendListParts(nil, &r), &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if diff := cmp.Diff(want, got, cmpopts.IgnoreFields(rtListParts{}, "XMLName")); diff != "" {
		t.Errorf("round trip mismatch (-want +got):\n%s", diff)
	}
}

func TestRoundTripListMultipartUploads(t *testing.T) {
	keys := awkwardKeys()
	r := ListMultipartUploadsResult{
		Bucket:             "b",
		KeyMarker:          "k&",
		UploadIDMarker:     "u<",
		NextKeyMarker:      "k>",
		NextUploadIDMarker: "u\"",
		Prefix:             "p&",
		Delimiter:          "/",
		EncodingType:       "",
		MaxUploads:         len(keys),
		IsTruncated:        true,
	}
	want := rtListUploads{
		Bucket:             "b",
		KeyMarker:          "k&",
		UploadIDMarker:     "u<",
		NextKeyMarker:      "k>",
		NextUploadIDMarker: "u\"",
		Prefix:             "p&",
		Delimiter:          "/",
		MaxUploads:         len(keys),
		IsTruncated:        true,
	}
	for _, k := range keys {
		r.Uploads = append(r.Uploads, Upload{Key: k, UploadID: "u&", StorageClass: "STANDARD", Initiated: testTime})
		want.Uploads = append(want.Uploads, rtUpload{Key: k, UploadID: "u&", StorageClass: "STANDARD", Initiated: testTime})
		r.CommonPrefixes = append(r.CommonPrefixes, k)
		want.CommonPrefixes = append(want.CommonPrefixes, rtPrefix{Prefix: k})
	}

	var got rtListUploads
	if err := xml.Unmarshal(AppendListMultipartUploads(nil, &r), &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if diff := cmp.Diff(want, got, cmpopts.IgnoreFields(rtListUploads{}, "XMLName")); diff != "" {
		t.Errorf("round trip mismatch (-want +got):\n%s", diff)
	}
}

func TestRoundTripError(t *testing.T) {
	in := ErrorResponse{Code: "InvalidArgument", Message: "bad <value> & \"x\"\r", Resource: "/b/k&", RequestID: "r&1", HostID: "h"}
	want := rtError{
		Code: in.Code, Message: in.Message, Resource: in.Resource, RequestID: in.RequestID, HostID: in.HostID,
	}
	var got rtError
	if err := xml.Unmarshal(AppendError(nil, &in), &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if diff := cmp.Diff(want, got, cmpopts.IgnoreFields(rtError{}, "XMLName")); diff != "" {
		t.Errorf("round trip mismatch (-want +got):\n%s", diff)
	}
	if got.XMLName.Space != "" {
		t.Errorf("error root namespace = %q, want none", got.XMLName.Space)
	}
}
