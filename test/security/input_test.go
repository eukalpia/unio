package security

import (
	"bytes"
	"context"
	"fmt"
	"net/http"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/s3"
)

// TestTraversalLookingKeysAreOpaque stores keys that look like filesystem
// paths and checks that each round-trips exactly and touches nothing else.
func TestTraversalLookingKeysAreOpaque(t *testing.T) {
	tg := loadTarget(t)
	bucket := tg.newBucket(t)
	keys := []string{
		"../../../../etc/passwd",
		"a/../b",
		"./dot",
		"..",
		"dir/./x",
		"%2e%2e/%2e%2e/encoded",
		"..\\..\\windows",
		"trailing/",
		"unicode/ключ/鍵",
	}
	ctx := context.Background()
	c := tg.sdk(t)
	for i, k := range keys {
		_, err := c.PutObject(ctx, &s3.PutObjectInput{
			Bucket: aws.String(bucket), Key: aws.String(k), Body: bytes.NewReader([]byte(fmt.Sprint(i))),
		})
		if err != nil {
			// Rejecting a key is acceptable; mis-storing it is not.
			t.Logf("key %q rejected: %v", k, err)
			continue
		}
		if got := tg.getObject(t, bucket, k); string(got) != fmt.Sprint(i) {
			t.Fatalf("key %q returned %q, want %q", k, got, fmt.Sprint(i))
		}
	}
	out, err := c.ListObjectsV2(ctx, &s3.ListObjectsV2Input{Bucket: aws.String(bucket)})
	if err != nil {
		t.Fatal(err)
	}
	var listed []string
	for _, o := range out.Contents {
		listed = append(listed, aws.ToString(o.Key))
	}
	sort.Strings(listed)
	for _, k := range listed {
		found := false
		for _, want := range keys {
			if k == want {
				found = true
			}
		}
		if !found {
			t.Fatalf("listing contains unexpected key %q: keys were rewritten", k)
		}
	}
}

func TestOversizedInputsAreRejected(t *testing.T) {
	tg := loadTarget(t)
	bucket := tg.newBucket(t)

	t.Run("key longer than 1024 bytes", func(t *testing.T) {
		key := strings.Repeat("k", 1025)
		req := newRequest(t, http.MethodPut, tg.objectURL(bucket, key, ""), []byte("x"))
		tg.sign(t, req, []byte("x"), tg.goodCreds(), time.Now(), "")
		expectRejected(t, send(t, req), "KeyTooLongError", "InvalidArgument")
	})

	t.Run("user metadata over 2 KiB", func(t *testing.T) {
		req := newRequest(t, http.MethodPut, tg.objectURL(bucket, "meta", ""), []byte("x"))
		req.Header.Set("X-Amz-Meta-Big", strings.Repeat("m", 3000))
		tg.sign(t, req, []byte("x"), tg.goodCreds(), time.Now(), "")
		expectRejected(t, send(t, req), "MetadataTooLarge")
		if tg.objectExists(t, bucket, "meta") {
			t.Fatal("object with oversized metadata was stored")
		}
	})

	t.Run("header section over 64 KiB", func(t *testing.T) {
		req := newRequest(t, http.MethodGet, tg.objectURL(bucket, "", ""), nil)
		for i := range 40 {
			req.Header.Set(fmt.Sprintf("X-Junk-%d", i), strings.Repeat("h", 4000))
		}
		tg.sign(t, req, nil, tg.goodCreds(), time.Now(), "")
		resp, err := httpClient().Do(req)
		if err == nil {
			resp.Body.Close()
			if resp.StatusCode < 400 {
				t.Fatalf("a 160 KiB header section was accepted with %d", resp.StatusCode)
			}
		}
		tg.mustAlive(t)
	})
}

func deleteObjectsRequest(t *testing.T, tg target, bucket string, body []byte) *http.Request {
	t.Helper()
	req := newRequest(t, http.MethodPost, tg.objectURL(bucket, "", "delete="), body)
	sum := md5Base64(body)
	req.Header.Set("Content-Md5", sum)
	req.Header.Set("Content-Type", "application/xml")
	tg.sign(t, req, body, tg.goodCreds(), time.Now(), "")
	return req
}

func TestHostileXMLBodies(t *testing.T) {
	tg := loadTarget(t)
	bucket := tg.newBucket(t)
	tg.putObject(t, bucket, "survivor", []byte("still here"))

	t.Run("entity expansion", func(t *testing.T) {
		body := []byte(`<?xml version="1.0"?>
<!DOCTYPE d [<!ENTITY a "aaaaaaaaaa"><!ENTITY b "&a;&a;&a;&a;&a;&a;&a;&a;&a;&a;"><!ENTITY c "&b;&b;&b;&b;&b;&b;&b;&b;&b;&b;">]>
<Delete><Object><Key>&c;</Key></Object></Delete>`)
		start := time.Now()
		expectRejected(t, send(t, deleteObjectsRequest(t, tg, bucket, body)), "MalformedXML", "InvalidRequest")
		if time.Since(start) > 10*time.Second {
			t.Fatal("entity expansion took too long to reject")
		}
	})

	t.Run("external entity", func(t *testing.T) {
		body := []byte(`<?xml version="1.0"?><!DOCTYPE d [<!ENTITY x SYSTEM "file:///etc/passwd">]><Delete><Object><Key>&x;</Key></Object></Delete>`)
		r := send(t, deleteObjectsRequest(t, tg, bucket, body))
		expectRejected(t, r, "MalformedXML", "InvalidRequest")
		if bytes.Contains(r.body, []byte("root:")) {
			t.Fatal("external entity was resolved")
		}
	})

	t.Run("deep nesting", func(t *testing.T) {
		depth := 200000
		body := []byte("<Delete>" + strings.Repeat("<a>", depth) + strings.Repeat("</a>", depth) + "</Delete>")
		start := time.Now()
		expectRejected(t, send(t, deleteObjectsRequest(t, tg, bucket, body)), "MalformedXML", "MaxMessageLengthExceeded", "InvalidRequest")
		if time.Since(start) > 10*time.Second {
			t.Fatal("deeply nested XML took too long to reject")
		}
	})

	t.Run("more than 1000 keys", func(t *testing.T) {
		var b strings.Builder
		b.WriteString("<Delete>")
		for i := range 1001 {
			fmt.Fprintf(&b, "<Object><Key>k%d</Key></Object>", i)
		}
		b.WriteString("</Delete>")
		expectRejected(t, send(t, deleteObjectsRequest(t, tg, bucket, []byte(b.String()))), "MalformedXML", "InvalidRequest", "InvalidArgument")
	})

	t.Run("body over the XML limit", func(t *testing.T) {
		// S3 sets no byte limit on DeleteObjects; Unio caps XML bodies.
		tg.unioOnly(t)
		body := []byte("<Delete><Object><Key>" + strings.Repeat("x", 3<<20) + "</Key></Object></Delete>")
		expectRejected(t, send(t, deleteObjectsRequest(t, tg, bucket, body)))
	})

	if !tg.objectExists(t, bucket, "survivor") {
		t.Fatal("a rejected DeleteObjects request deleted an object")
	}
	tg.mustAlive(t)
}

func TestReservedBucketName(t *testing.T) {
	tg := loadTarget(t)
	tg.unioOnly(t)
	req := newRequest(t, http.MethodPut, tg.objectURL("unio", "", ""), nil)
	tg.sign(t, req, nil, tg.goodCreds(), time.Now(), "")
	expectRejected(t, send(t, req), "InvalidBucketName")
}
