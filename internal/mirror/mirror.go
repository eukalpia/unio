// Package mirror copies buckets from one S3-compatible store to another and
// checks that the copy is complete. It is how a deployment moves from MinIO
// (or any S3 store) to Unio: run it while the old store still serves, run it
// again to pick up what changed, and switch when a verify pass finds no
// difference.
//
// Every object is checked on the way. When the source ETag is the MD5 of the
// object, it is sent as Content-MD5, so the destination refuses the upload
// unless it received exactly those bytes. For multipart objects, whose ETag
// is not a content hash, the copy is checked by size and by the MD5 computed
// while copying, and the source ETag is kept in the metadata key
// SourceETagKey so that a later run can tell an unchanged object from a
// changed one of the same size.
package mirror

import (
	"context"
	"crypto/md5" // #nosec G501 -- S3 ETags and Content-MD5 are MD5 by protocol, not a security check
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"maps"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"golang.org/x/sync/errgroup"
)

// SourceETagKey is the user metadata key that records the source ETag of a
// copied multipart object.
const SourceETagKey = "unio-mirror-source-etag"

// Object describes one object as listed or read.
type Object struct {
	Key                string
	Size               int64
	ETag               string // without quotes
	ContentType        string
	ContentDisposition string
	ContentEncoding    string
	ContentLanguage    string
	CacheControl       string
	Metadata           map[string]string
}

// Store is the subset of S3 that mirroring needs. Implementations must be
// safe for concurrent use.
type Store interface {
	ListBuckets(ctx context.Context) ([]string, error)
	// EnsureBucket creates bucket unless it exists.
	EnsureBucket(ctx context.Context, bucket string) error
	// List calls fn for every object of bucket in key order.
	List(ctx context.Context, bucket string, fn func(Object) error) error
	// Head returns the object, or false when it does not exist.
	Head(ctx context.Context, bucket, key string) (Object, bool, error)
	Get(ctx context.Context, bucket, key string) (io.ReadCloser, Object, error)
	// Put stores body as obj. contentMD5 is the base64 MD5 to send as
	// Content-MD5, or "" to send none.
	Put(ctx context.Context, bucket string, obj Object, body io.Reader, contentMD5 string) error
	// GetPolicy returns the bucket policy, or nil when there is none.
	GetPolicy(ctx context.Context, bucket string) ([]byte, error)
	PutPolicy(ctx context.Context, bucket string, doc []byte) error
}

// Options controls a run.
type Options struct {
	// Buckets limits the run to these buckets. Empty means every bucket of
	// the source.
	Buckets []string
	// Workers is the number of objects copied at once. Zero selects 8.
	Workers int
	// Verify compares only: nothing is written to the destination.
	Verify bool
	// SkipPolicies leaves bucket policies alone.
	SkipPolicies bool
	// Attempts is how often a failing object is tried. Zero selects 3.
	Attempts int
	// Logger receives progress and failures. Nil uses slog.Default.
	Logger *slog.Logger
}

// Report is the outcome of a run.
type Report struct {
	Buckets int
	Objects int64 // objects seen in the source
	Copied  int64
	Skipped int64 // already present and equal
	Bytes   int64 // bytes copied
	// Missing and Different are found by a verify run.
	Missing   int64
	Different int64
	// Extra counts destination objects absent from the source. They are
	// reported, never deleted.
	Extra    int64
	Failed   int64
	Failures []string // the first maxFailures failures
	Policies int      // bucket policies copied or found equal
}

// OK reports whether the destination now matches the source.
func (r Report) OK() bool {
	return r.Failed == 0 && r.Missing == 0 && r.Different == 0
}

const maxFailures = 100

// Run mirrors src into dst.
func Run(ctx context.Context, src, dst Store, opts Options) (Report, error) {
	if opts.Workers <= 0 {
		opts.Workers = 8
	}
	if opts.Attempts <= 0 {
		opts.Attempts = 3
	}
	if opts.Logger == nil {
		opts.Logger = slog.Default()
	}
	buckets := opts.Buckets
	if len(buckets) == 0 {
		all, err := src.ListBuckets(ctx)
		if err != nil {
			return Report{}, fmt.Errorf("mirror: list source buckets: %w", err)
		}
		buckets = all
	}
	r := &runner{src: src, dst: dst, opts: opts}
	for _, b := range buckets {
		if err := r.bucket(ctx, b); err != nil {
			return r.report(), err
		}
	}
	return r.report(), nil
}

type runner struct {
	src, dst Store
	opts     Options

	buckets, policies                 int
	objects, copied, skipped, bytes   atomic.Int64
	missing, different, extra, failed atomic.Int64
	mu                                sync.Mutex
	failures                          []string
}

func (r *runner) report() Report {
	r.mu.Lock()
	defer r.mu.Unlock()
	return Report{
		Buckets: r.buckets, Objects: r.objects.Load(), Copied: r.copied.Load(), Skipped: r.skipped.Load(),
		Bytes: r.bytes.Load(), Missing: r.missing.Load(), Different: r.different.Load(), Extra: r.extra.Load(),
		Failed: r.failed.Load(), Failures: slices.Clone(r.failures), Policies: r.policies,
	}
}

func (r *runner) fail(bucket, key string, err error) {
	r.failed.Add(1)
	msg := fmt.Sprintf("%s/%s: %v", bucket, key, err)
	r.opts.Logger.Error("object not mirrored", "bucket", bucket, "key", key, "err", err)
	r.mu.Lock()
	if len(r.failures) < maxFailures {
		r.failures = append(r.failures, msg)
	}
	r.mu.Unlock()
}

func (r *runner) bucket(ctx context.Context, bucket string) error {
	log := r.opts.Logger.With("bucket", bucket)
	if !r.opts.Verify {
		if err := r.dst.EnsureBucket(ctx, bucket); err != nil {
			return fmt.Errorf("mirror: create bucket %s: %w", bucket, err)
		}
	}
	r.buckets++
	if !r.opts.SkipPolicies {
		if err := r.policy(ctx, bucket); err != nil {
			return err
		}
	}

	seen := make(map[string]struct{})
	var seenMu sync.Mutex
	g, gctx := errgroup.WithContext(ctx)
	g.SetLimit(r.opts.Workers)
	err := r.src.List(gctx, bucket, func(o Object) error {
		r.objects.Add(1)
		seenMu.Lock()
		seen[o.Key] = struct{}{}
		seenMu.Unlock()
		g.Go(func() error {
			r.object(gctx, bucket, o)
			return nil
		})
		return gctx.Err()
	})
	if werr := g.Wait(); err == nil {
		err = werr
	}
	if err != nil {
		return fmt.Errorf("mirror: list %s: %w", bucket, err)
	}

	// Objects only the destination has are reported, never deleted: the
	// destination may already take writes of its own.
	err = r.dst.List(ctx, bucket, func(o Object) error {
		if _, ok := seen[o.Key]; !ok {
			r.extra.Add(1)
			if r.opts.Verify {
				log.Warn("object only in the destination", "key", o.Key)
			}
		}
		return nil
	})
	if err != nil {
		log.Warn("could not list the destination to count extra objects", "err", err)
	}
	log.Info("bucket done", "objects", len(seen))
	return nil
}

// policy copies the bucket policy, or in a verify run compares it.
func (r *runner) policy(ctx context.Context, bucket string) error {
	want, err := r.src.GetPolicy(ctx, bucket)
	if err != nil {
		return fmt.Errorf("mirror: read the policy of %s: %w", bucket, err)
	}
	if want == nil {
		return nil
	}
	have, err := r.dst.GetPolicy(ctx, bucket)
	if err != nil && !r.opts.Verify {
		return fmt.Errorf("mirror: read the destination policy of %s: %w", bucket, err)
	}
	if string(have) == string(want) {
		r.policies++
		return nil
	}
	if r.opts.Verify {
		r.different.Add(1)
		r.opts.Logger.Warn("bucket policy differs", "bucket", bucket)
		return nil
	}
	if err := r.dst.PutPolicy(ctx, bucket, want); err != nil {
		return fmt.Errorf("mirror: the destination refused the policy of %s: %w", bucket, err)
	}
	r.policies++
	return nil
}

// object brings one object up to date, or in a verify run compares it.
func (r *runner) object(ctx context.Context, bucket string, o Object) {
	have, ok, err := r.dst.Head(ctx, bucket, o.Key)
	if err != nil {
		r.fail(bucket, o.Key, fmt.Errorf("head destination: %w", err))
		return
	}
	if ok && same(o, have) {
		r.skipped.Add(1)
		return
	}
	if r.opts.Verify {
		if !ok {
			r.missing.Add(1)
			r.opts.Logger.Warn("object missing in the destination", "bucket", bucket, "key", o.Key)
		} else {
			r.different.Add(1)
			r.opts.Logger.Warn("object differs", "bucket", bucket, "key", o.Key, "sourceSize", o.Size, "destinationSize", have.Size)
		}
		return
	}
	var last error
	for attempt := 1; attempt <= r.opts.Attempts; attempt++ {
		n, err := r.copy(ctx, bucket, o.Key)
		if err == nil {
			r.copied.Add(1)
			r.bytes.Add(n)
			return
		}
		last = err
		if ctx.Err() != nil || errors.Is(err, errChanged) {
			break
		}
		select {
		case <-ctx.Done():
		case <-time.After(time.Duration(attempt) * 500 * time.Millisecond):
		}
	}
	r.fail(bucket, o.Key, last)
}

// errChanged reports a source object that changed while it was copied.
var errChanged = errors.New("the source object changed while it was copied; run again")

// copy streams one object and checks it. The object is read again rather
// than taken from the listing, so its metadata is current.
func (r *runner) copy(ctx context.Context, bucket, key string) (int64, error) {
	body, o, err := r.src.Get(ctx, bucket, key)
	if err != nil {
		return 0, fmt.Errorf("read source: %w", err)
	}
	defer body.Close()

	meta := maps.Clone(o.Metadata)
	delete(meta, SourceETagKey)
	var contentMD5 string
	if plainMD5(o.ETag) {
		raw, _ := hex.DecodeString(o.ETag)
		contentMD5 = base64.StdEncoding.EncodeToString(raw)
	} else if o.ETag != "" {
		if meta == nil {
			meta = make(map[string]string, 1)
		}
		meta[SourceETagKey] = o.ETag
	}
	put := o
	put.Key = key
	put.Metadata = meta

	h := md5.New() // #nosec G401 -- checks the copy against the S3 ETag
	counted := &countingReader{r: io.TeeReader(body, h)}
	if err := r.dst.Put(ctx, bucket, put, counted, contentMD5); err != nil {
		return 0, fmt.Errorf("write destination: %w", err)
	}
	if counted.n != o.Size {
		return 0, fmt.Errorf("copied %d bytes of %d: %w", counted.n, o.Size, errChanged)
	}
	if plainMD5(o.ETag) && hex.EncodeToString(h.Sum(nil)) != strings.ToLower(o.ETag) {
		return 0, fmt.Errorf("content MD5 differs from the source ETag: %w", errChanged)
	}
	got, ok, err := r.dst.Head(ctx, bucket, key)
	if err != nil {
		return 0, fmt.Errorf("check destination: %w", err)
	}
	if !ok || got.Size != o.Size {
		return 0, errors.New("the destination does not hold the object after the write")
	}
	return counted.n, nil
}

// same reports whether the destination object dst is an up-to-date copy of
// src. Sizes must match; a plain MD5 ETag must match; a multipart ETag must
// match the one recorded when the object was copied.
func same(src, dst Object) bool {
	if src.Size != dst.Size {
		return false
	}
	if plainMD5(src.ETag) {
		return strings.EqualFold(src.ETag, dst.ETag)
	}
	if src.ETag == "" {
		return true
	}
	return src.ETag == dst.Metadata[SourceETagKey] || src.ETag == dst.ETag
}

// plainMD5 reports whether etag is the hex MD5 of the object, which is the
// case for objects uploaded in one request without SSE-KMS or SSE-C.
func plainMD5(etag string) bool {
	if len(etag) != 32 {
		return false
	}
	_, err := hex.DecodeString(etag)
	return err == nil
}

type countingReader struct {
	r io.Reader
	n int64
}

func (c *countingReader) Read(p []byte) (int, error) {
	n, err := c.r.Read(p)
	c.n += int64(n)
	return n, err
}
