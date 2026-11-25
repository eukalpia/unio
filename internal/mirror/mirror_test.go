package mirror

import (
	"bytes"
	"context"
	"crypto/md5" // #nosec G501 -- test of S3 ETags
	"encoding/base64"
	"encoding/hex"
	"errors"
	"io"
	"maps"
	"slices"
	"sort"
	"strings"
	"sync"
	"testing"
)

// memStore is an in-memory Store. It checks Content-MD5 like S3 does.
type memStore struct {
	mu       sync.Mutex
	buckets  map[string]map[string]memObject
	policies map[string][]byte
	// corrupt flips a byte of every body read from this store.
	corrupt bool
	puts    int
}

type memObject struct {
	obj  Object
	data []byte
}

func newMem() *memStore {
	return &memStore{buckets: map[string]map[string]memObject{}, policies: map[string][]byte{}}
}

func (m *memStore) add(bucket, key, data string, meta map[string]string, multipartETag string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.buckets[bucket] == nil {
		m.buckets[bucket] = map[string]memObject{}
	}
	sum := md5.Sum([]byte(data)) // #nosec G401 -- S3 ETag
	etag := hex.EncodeToString(sum[:])
	if multipartETag != "" {
		etag = multipartETag
	}
	m.buckets[bucket][key] = memObject{
		obj:  Object{Key: key, Size: int64(len(data)), ETag: etag, ContentType: "application/pdf", ContentDisposition: `attachment; filename="a.pdf"`, Metadata: meta},
		data: []byte(data),
	}
}

func (m *memStore) ListBuckets(context.Context) ([]string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return slices.Sorted(maps.Keys(m.buckets)), nil
}

func (m *memStore) EnsureBucket(_ context.Context, b string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.buckets[b] == nil {
		m.buckets[b] = map[string]memObject{}
	}
	return nil
}

func (m *memStore) List(_ context.Context, b string, fn func(Object) error) error {
	m.mu.Lock()
	objs, ok := m.buckets[b]
	if !ok {
		m.mu.Unlock()
		return errors.New("NoSuchBucket")
	}
	keys := slices.Sorted(maps.Keys(objs))
	list := make([]Object, 0, len(keys))
	for _, k := range keys {
		list = append(list, objs[k].obj)
	}
	m.mu.Unlock()
	for _, o := range list {
		if err := fn(o); err != nil {
			return err
		}
	}
	return nil
}

func (m *memStore) Head(_ context.Context, b, k string) (Object, bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	o, ok := m.buckets[b][k]
	return o.obj, ok, nil
}

func (m *memStore) Get(_ context.Context, b, k string) (io.ReadCloser, Object, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	o, ok := m.buckets[b][k]
	if !ok {
		return nil, Object{}, errors.New("NoSuchKey")
	}
	data := bytes.Clone(o.data)
	if m.corrupt && len(data) > 0 {
		data[0] ^= 1
	}
	return io.NopCloser(bytes.NewReader(data)), o.obj, nil
}

func (m *memStore) Put(_ context.Context, b string, o Object, body io.Reader, contentMD5 string) error {
	data, err := io.ReadAll(body)
	if err != nil {
		return err
	}
	sum := md5.Sum(data) // #nosec G401 -- S3 ETag
	if contentMD5 != "" && base64.StdEncoding.EncodeToString(sum[:]) != contentMD5 {
		return errors.New("BadDigest")
	}
	if int64(len(data)) != o.Size {
		return errors.New("IncompleteBody")
	}
	o.ETag = hex.EncodeToString(sum[:])
	o.Metadata = maps.Clone(o.Metadata)
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.buckets[b] == nil {
		return errors.New("NoSuchBucket")
	}
	m.buckets[b][o.Key] = memObject{obj: o, data: data}
	m.puts++
	return nil
}

func (m *memStore) GetPolicy(_ context.Context, b string) ([]byte, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.policies[b], nil
}

func (m *memStore) PutPolicy(_ context.Context, b string, doc []byte) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.policies[b] = bytes.Clone(doc)
	return nil
}

func run(t *testing.T, src, dst *memStore, opts Options) Report {
	t.Helper()
	rep, err := Run(t.Context(), src, dst, opts)
	if err != nil {
		t.Fatal(err)
	}
	return rep
}

func TestMirrorCopiesEverythingAndThenNothing(t *testing.T) {
	src, dst := newMem(), newMem()
	src.add("lms", "materials/a.pdf", "lecture one", map[string]string{"owner": "42"}, "")
	src.add("lms", "materials/b.pdf", "lecture two", nil, "")
	src.add("lms", "video/v.mp4", "a big video", nil, "0123456789abcdef0123456789abcdef-3")
	src.add("admission", "users/1/photo.jpg", "face", nil, "")
	src.policies["admission"] = []byte(`{"Version":"2012-10-17"}`)

	rep := run(t, src, dst, Options{})
	if !rep.OK() || rep.Copied != 4 || rep.Skipped != 0 || rep.Buckets != 2 || rep.Policies != 1 {
		t.Fatalf("first run: %+v", rep)
	}
	got := dst.buckets["lms"]["materials/a.pdf"]
	if string(got.data) != "lecture one" || got.obj.ContentType != "application/pdf" ||
		got.obj.ContentDisposition != `attachment; filename="a.pdf"` || got.obj.Metadata["owner"] != "42" {
		t.Fatalf("copy lost data or headers: %+v", got.obj)
	}
	if dst.buckets["lms"]["video/v.mp4"].obj.Metadata[SourceETagKey] != "0123456789abcdef0123456789abcdef-3" {
		t.Fatal("multipart source ETag not recorded")
	}
	if string(dst.policies["admission"]) != `{"Version":"2012-10-17"}` {
		t.Fatal("bucket policy not copied")
	}

	puts := dst.puts
	rep = run(t, src, dst, Options{})
	if !rep.OK() || rep.Copied != 0 || rep.Skipped != 4 || dst.puts != puts {
		t.Fatalf("second run copied again: %+v", rep)
	}
	rep = run(t, src, dst, Options{Verify: true})
	if !rep.OK() || rep.Skipped != 4 {
		t.Fatalf("verify after a complete copy: %+v", rep)
	}
}

func TestMirrorCopiesWhatChanged(t *testing.T) {
	src, dst := newMem(), newMem()
	src.add("lms", "a.pdf", "v1", nil, "")
	src.add("lms", "v.mp4", "same size", nil, "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa-2")
	run(t, src, dst, Options{})

	src.add("lms", "a.pdf", "v2", nil, "")                                          // same size, new content
	src.add("lms", "v.mp4", "same SIZE", nil, "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb-2") // multipart, same size
	src.add("lms", "new.pdf", "new", nil, "")

	verify := run(t, src, dst, Options{Verify: true})
	if verify.OK() || verify.Missing != 1 || verify.Different != 2 {
		t.Fatalf("verify did not see the changes: %+v", verify)
	}
	if dst.puts != 2 {
		t.Fatal("verify wrote to the destination")
	}
	rep := run(t, src, dst, Options{})
	if !rep.OK() || rep.Copied != 3 {
		t.Fatalf("changed objects not copied: %+v", rep)
	}
	if string(dst.buckets["lms"]["a.pdf"].data) != "v2" || string(dst.buckets["lms"]["v.mp4"].data) != "same SIZE" {
		t.Fatal("destination still holds the old content")
	}
}

// Bytes damaged between the stores must never be stored: for a plain ETag the
// destination refuses them through Content-MD5.
func TestMirrorRefusesDamagedBytes(t *testing.T) {
	src, dst := newMem(), newMem()
	src.add("lms", "a.pdf", "important", nil, "")
	src.corrupt = true
	rep := run(t, src, dst, Options{Attempts: 2})
	if rep.OK() || rep.Failed != 1 || len(rep.Failures) != 1 {
		t.Fatalf("damaged copy not reported: %+v", rep)
	}
	if _, ok := dst.buckets["lms"]["a.pdf"]; ok {
		t.Fatal("damaged bytes were stored")
	}
	if !strings.Contains(rep.Failures[0], "lms/a.pdf") {
		t.Fatalf("failure does not name the object: %q", rep.Failures[0])
	}
}

func TestMirrorReportsExtraObjectsWithoutDeleting(t *testing.T) {
	src, dst := newMem(), newMem()
	src.add("lms", "a.pdf", "a", nil, "")
	dst.add("lms", "written-after-switch.pdf", "b", nil, "")
	rep := run(t, src, dst, Options{})
	if rep.Extra != 1 || !rep.OK() {
		t.Fatalf("extra object: %+v", rep)
	}
	if _, ok := dst.buckets["lms"]["written-after-switch.pdf"]; !ok {
		t.Fatal("an object only the destination had was deleted")
	}
}

func TestMirrorOnlySelectedBuckets(t *testing.T) {
	src, dst := newMem(), newMem()
	src.add("lms", "a", "a", nil, "")
	src.add("backups", "b", "b", nil, "")
	run(t, src, dst, Options{Buckets: []string{"lms"}})
	names, _ := dst.ListBuckets(t.Context())
	sort.Strings(names)
	if !slices.Equal(names, []string{"lms"}) {
		t.Fatalf("buckets in the destination: %v", names)
	}
}
