# Artifact Versioning

`packages/artifacts` is the format-neutral versioning substrate for Dev Plane.

It deliberately separates the **revision graph** from **payload storage**:

- Git/Jujutsu remain the forge-compatible source-control layer.
- Artifact manifests describe logical files and their immutable content digests.
- A `BlobStore` stores byte-exact payloads outside Git.
- `LocalStore` provides a filesystem content-addressed store for development and
  node-local worker caches.
- Cloud stores (R2/S3/MinIO), chunking, semantic adapters, previews, and leases
  can implement or build on these primitives without changing manifest/version
  identity.
- Git LFS pointer parsing/encoding provides an interoperability bridge for
  large objects while Dev Plane's CAS remains canonical.

## Object model

```text
Version
  -> Manifest digest
       -> Artifact path
            -> original Descriptor
            -> optional chunks
            -> optional semantic representation
            -> optional derivatives
```

Original payload bytes remain authoritative. Derived text, previews, proxies,
transcripts, thumbnails, or semantic ASTs are separate descriptors.

## Digests

The first schema version uses SHA-256 to keep object identity interoperable with
Git LFS, OCI-style descriptors, object stores, and existing security tooling.
The `Digest` type carries its algorithm so additional algorithms can be added
without changing higher-level APIs.

## Git LFS compatibility

`LFSPointer` emits and parses standard pointer bodies:

```text
version https://git-lfs.github.com/spec/v1
oid sha256:<digest>
size <bytes>
```

This does not make Git LFS the source of truth. It lets a repository expose a
normal Git-compatible pointer while the payload is resolved from Dev Plane's
artifact store.

## S3 / R2 storage

`S3Store` is a real S3-compatible `BlobStore` implemented with the Go standard
library and AWS Signature Version 4. It keeps the artifact core free of a cloud
SDK dependency while supporting AWS S3, Cloudflare R2, MinIO, and compatible
path-style endpoints.

For Cloudflare R2:

```go
store, err := NewS3Store(S3StoreConfig{
    Endpoint:        "https://<account-id>.r2.cloudflarestorage.com",
    Region:          "auto",
    Bucket:          "dev-plane-artifacts",
    Prefix:          "cas",
    AccessKeyID:     os.Getenv("R2_ACCESS_KEY_ID"),
    SecretAccessKey: os.Getenv("R2_SECRET_ACCESS_KEY"),
})
```

Uploads are staged to a temporary file while SHA-256 is computed, so large
payloads are never buffered fully in memory. The resulting object key is derived
only from its digest and uploads are de-duplicated with a HEAD check.

The first implementation uses single-object PUTs. Very large media should be
split through the artifact chunk model; multipart/chunk orchestration is the
next storage optimization rather than making Git carry large binaries.

## Content-defined chunking

Large payloads can be stored through `ContentDefinedChunker`. It uses a
FastCDC-inspired gear hash with configurable minimum, average, and maximum
chunk sizes. Chunk boundaries depend on content rather than absolute offsets,
so small insertions or deletions only invalidate nearby chunks.

The default policy is:

```text
minimum: 512 KiB
average:   2 MiB
maximum:   8 MiB
```

`Manager.PutChunkedArtifact` stores each chunk independently in the CAS while
the artifact descriptor retains the SHA-256 identity and size of the complete
byte stream. `Manager.Materialize` transparently reconstructs chunked
artifacts. With an S3/R2 store, already-present chunks are skipped through the
same CAS deduplication path.

## Source + artifact snapshots

The VCS and runtime snapshot types can carry both source-control identity and
artifact identity:

```text
Git commit / Jujutsu change
        +
artifact manifest digest
        +
artifact version digest
        =
one reviewable workspace snapshot
```

Dev Plane persists these bindings in `workspace_snapshots`, so reviews,
restores, provenance, and later candidate verification can refer to the exact
code and non-code state together.

## Next integrations

1. Wire artifact manifests into VCS snapshots/workspace metadata.
2. Add an R2/S3 `BlobStore`.
3. Add large-object chunk manifests and content-defined chunking.
4. Add MIME-aware document/PDF/image adapters.
5. Add short-lived write leases for non-mergeable formats.
