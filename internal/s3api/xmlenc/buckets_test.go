package xmlenc

import (
	"testing"
	"time"
)

func TestAppendListBuckets(t *testing.T) {
	tests := []struct {
		name string
		in   ListBucketsResult
		want string
	}{
		{
			name: "owner and buckets with UTC conversion",
			in: ListBucketsResult{
				Owner: Owner{ID: "abc", DisplayName: "alice"},
				Buckets: []Bucket{
					{Name: "b1", CreationDate: testTime},
					{Name: "b&2", CreationDate: time.Date(2025, 12, 31, 23, 0, 0, 0, time.FixedZone("UTC+1", 3600))},
				},
			},
			want: wantDecl + `<ListAllMyBucketsResult ` + wantNS + `>` +
				`<Owner><ID>abc</ID><DisplayName>alice</DisplayName></Owner>` +
				`<Buckets>` +
				`<Bucket><Name>b1</Name><CreationDate>2026-01-02T03:04:05.600Z</CreationDate></Bucket>` +
				`<Bucket><Name>b&amp;2</Name><CreationDate>2025-12-31T22:00:00.000Z</CreationDate></Bucket>` +
				`</Buckets>` +
				`</ListAllMyBucketsResult>`,
		},
		{
			name: "no buckets keeps the wrapper element",
			in:   ListBucketsResult{Owner: Owner{ID: "x"}},
			want: wantDecl + `<ListAllMyBucketsResult ` + wantNS + `>` +
				`<Owner><ID>x</ID><DisplayName></DisplayName></Owner><Buckets></Buckets>` +
				`</ListAllMyBucketsResult>`,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			checkAppend(t, tc.want, func(dst []byte) []byte { return AppendListBuckets(dst, &tc.in) })
		})
	}
}

func TestAppendLocationConstraint(t *testing.T) {
	tests := []struct {
		name   string
		region string
		want   string
	}{
		{name: "region", region: "eu-west-1", want: wantDecl + `<LocationConstraint ` + wantNS + `>eu-west-1</LocationConstraint>`},
		{name: "empty region", region: "", want: wantDecl + `<LocationConstraint ` + wantNS + `></LocationConstraint>`},
		{name: "region is escaped", region: "a&b", want: wantDecl + `<LocationConstraint ` + wantNS + `>a&amp;b</LocationConstraint>`},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			checkAppend(t, tc.want, func(dst []byte) []byte { return AppendLocationConstraint(dst, tc.region) })
		})
	}
}
