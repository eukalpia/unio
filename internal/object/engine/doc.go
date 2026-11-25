// Package engine implements object.Layer on top of a metadata store
// (meta.Store) and a registry of erasure sets (erasure.Registry).
//
// Data placement follows the size tiers of ADR-0005. Payloads up to the
// inline threshold are kept in the metadata record; a disabled inline tier
// (Options.InlineThreshold below zero) keeps none. Larger payloads, and every
// multipart part, are written to one erasure set and referenced by a
// meta.PartRecord. The metadata commit is the only point where a write becomes
// visible, and data is deleted only after the metadata that refers to it has
// gone (I4, I5). Deletions run in the background (package gc). A deletion that
// cannot be queued or fails is logged and left to the scrubber.
//
// Invariant enforcement:
//
//   - I1 (durability): a PUT returns only after the erasure set reported the
//     write quorum and the metadata store committed. Data durability is the
//     erasure set's job; the engine never acknowledges a write before both steps.
//   - I2 (integrity): reads go through erasure.Set.Read, which verifies every
//     block. A reader never returns data after an error, and it fails rather
//     than spin when a part reader stops making progress (chain.go). Inline
//     payloads are checked when they were stored.
//   - I3 (linearizable metadata): all visibility decisions are made by
//     meta.Store, which is linearizable. The engine holds no cache of metadata.
//   - I4 (atomicity): see bodyGuard in body.go and writePart in put.go. A body
//     that did not reach a clean io.EOF with exactly Size bytes never reaches
//     the commit. Data written before such a failure is queued for deletion.
//   - I5 (safe overwrite): every commit returns the versions and parts that it
//     made garbage, and the engine queues their data only after the commit
//     succeeded.
//
// Failed commits. A commit error does not always mean that the commit was not
// applied: a consensus store can apply a proposal and then time out. Data of a
// failed commit is queued for deletion only when the error is one of the
// store's definitive refusals (PreconditionFailed, NoSuchKey, NoSuchBucket,
// NoSuchUpload, InvalidPart; see definitelyNotCommitted). For any other error
// the data is kept and logged, and the scrubber decides whether it is garbage
// by checking the metadata. A rejected write whose bucket changed versioning
// state meanwhile is reported as ConditionalRequestConflict, which is
// retryable.
//
// Semantics the contract leaves open, and the choices made here:
//
//   - Delete markers. A read whose latest version is a delete marker returns
//     NoSuchKey. The error is a *s3err.Error whose cause exposes the marker, so
//     the front end can set x-amz-delete-marker. The cause implements
//     DeleteMarkerInfo() object.Info; see marker.go. A read that names a delete
//     marker version explicitly returns MethodNotAllowed.
//   - PartNumber. For a single-part object, part 1 is the whole object. For a
//     multipart object, the part with that number. Info.Size and the reader
//     window then refer to the selected part, and Info.Parts gives the count.
//     Range applies inside the selected part.
//   - Copy sources. A failed copy-source precondition is PreconditionFailed,
//     even for If-None-Match, as in S3. A copy whose source is larger than
//     MaxPutSize is InvalidRequest. CopyObject rejects a range; only
//     CopyObjectPart accepts one.
//   - Version ids. The engine reads the bucket's versioning state before each
//     write. Enabled buckets get a fresh random version id, all others use the
//     null version. The store does not assign version ids itself.
//   - Checksums. An upload with a checksum algorithm needs every part to carry
//     that algorithm, or the completion fails with InvalidPart. Multipart
//     objects always get a composite checksum, for every algorithm.
//   - Forced bucket deletion. Only unversioned buckets are emptied. A versioned
//     or suspended bucket with current objects or uploads is refused with
//     BucketNotEmpty before anything changes (bucket.go).
//   - Bodies. A nil body is InvalidArgument. A body that ends early is
//     IncompleteBody, whatever error the erasure set wrapped around it.
//
// Concurrency and resources. Each erasure-coded write holds a slot from
// Options.MaxConcurrentWrites, and the slot is held while the body is read.
// DeleteObjects runs at most Options.DeleteConcurrency deletions per call.
// Pooled buffers for inline payloads come from a bufpool budget. The deletion
// queue has a fixed backlog and a fixed worker count. No goroutine is started
// per request.
//
// Known gaps. A read that is in progress when an overwrite commits can fail
// once the old shards are deleted; the engine has no reader leases. Deletions
// that are dropped or fail are reclaimed only by the scrubber, which this
// package does not provide. Force bucket deletion does not remove old versions
// or delete markers of versioned buckets, because the store does not list them.
// A slow client holds an erasure write slot while it sends its body; the front
// end's body timeouts bound that.
package engine
