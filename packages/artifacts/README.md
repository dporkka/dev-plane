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

Uploads through the server-side `BlobStore` path are staged to a temporary
file while SHA-256 is computed, so payloads are never buffered fully in memory.
The resulting object key is derived only from its digest and uploads are
de-duplicated with a HEAD check.

Very large media can bypass API-worker bandwidth through the direct multipart
data plane. The API creates a random staging key and S3/R2 multipart upload,
returns short-lived presigned `UploadPart` URLs in bounded batches, and persists
the upload session in SQL. After the client completes all parts, Dev Plane
streams the staged object through exact SHA-256 and size verification, promotes
the verified object to its canonical CAS key with server-side `CopyObject`, and
then runs normal semantic analysis/versioning.

The TypeScript SDK exposes `uploadWorkspaceArtifactMultipart`, which adds:

- streaming/incremental SHA-256 without materializing a whole large Blob,
- bounded parallel part uploads,
- exponential retry for transient part failures,
- automatic presigned-URL refresh after a 403,
- durable caller-owned checkpoints for resumable uploads,
- progress callbacks and `AbortSignal` cancellation,
- ordered ETag collection and automatic completion,
- optional automatic abort/cleanup on terminal errors.

Browser buckets must allow the application origin to `PUT` to presigned object
URLs and must expose the `ETag` response header through CORS. The uploader
rejects a successful part response with no visible ETag because multipart
completion cannot be made reliable without it.

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

## Format-aware analysis

`AdapterRegistry` turns opaque binary payloads into reproducible derived
representations without replacing the original bytes.

The default registry currently includes:

- **DOCX** — detects OOXML content, extracts paragraph-structured semantic JSON,
  and stores it as a semantic derivative.
- **XLSX** — resolves workbook relationships/shared strings and extracts ordered
  sheets, cell references, values, types, and formulas.
- **PPTX** — resolves presentation relationship order and extracts slide-by-slide
  paragraph text.
- **Images** — detects common image formats, records dimensions/format, stores
  semantic JSON, and creates a bounded PNG thumbnail.
- **PDF** — uses a pluggable `PDFToolRunner`. The default Poppler-backed runner
  extracts text with `pdftotext` and renders the first page with `pdftoppm`.
  If those tools are not installed, analysis degrades gracefully instead of
  invalidating the source artifact.

Analysis always operates on a temporary materialization of the immutable
artifact. Generated semantics and previews are stored back into the same CAS
and attached by digest:

```text
original.docx
  sha256:ORIGINAL
      |
      +-- semantic-document -> sha256:SEMANTIC_JSON

hero.png
  sha256:ORIGINAL
      |
      +-- semantic-image    -> sha256:IMAGE_METADATA
      +-- preview-thumbnail -> sha256:THUMBNAIL

report.pdf
  sha256:ORIGINAL
      |
      +-- semantic-text     -> sha256:EXTRACTED_TEXT
      +-- preview-page-1    -> sha256:PAGE_PREVIEW
```

The adapter and adapter version are recorded in artifact metadata and on each
derivative's generator record, making derived representations reproducible and
safe to invalidate when an adapter changes.

## Semantic diffs and collaboration policy

`Manager.DiffArtifacts` compares the richest common semantic representation
available rather than diffing binary bytes:

- DOCX semantic JSON is compared paragraph-by-paragraph.
- XLSX semantics are compared as stable `Sheet!Cell = value [formula]` sequences.
- PPTX semantics are compared as ordered slide/paragraph sequences.
- PDF extracted text is compared line-by-line.
- Images compare dimensions, decoded format, and preview identity.
- Artifacts without a common semantic derivative fall back to immutable payload
  digests and explicitly report that no semantic diff was available.

Normal documents use a bounded LCS diff. Extremely large semantic sequences
fall back to a prefix/suffix-bounded algorithm so an adversarial document cannot
force unbounded diff memory.

`DefaultCollaborationPolicy` is deliberately conservative about merge support.
Text can use ordinary three-way merge. Formats for which Dev Plane does not yet
have a lossless merge engine require a short-lived write lease, while still
allowing agents to fork speculative variants.

## Artifact write leases

Both local and durable lease stores implement the same `LeaseStore` contract:

```text
Acquire(scope, path, owner, ttl)
Renew(lease, ttl)
Release(lease)
Get(scope, path)
```

Lease grants carry a random capability token and a monotonic generation. The
generation prevents an old agent from accidentally becoming valid again after
a lease expires and is re-acquired (the ABA problem).

`MemoryLeaseStore` is intended for tests/local execution. `SQLLeaseStore`
uses the `artifact_leases` table for swarm coordination on SQLite or
PostgreSQL. Acquisition is one atomic upsert conditioned on expiry, so competing
workers cannot both acquire the same path. Only SHA-256 hashes of capability
tokens are persisted; raw tokens remain with the lease holder.

The maximum lease TTL is 24 hours. Normal agent workflows should use much
shorter leases and renew them while work is active.

## Exact artifact-tree restore

Workspace artifact history is append-only. Deleting an artifact appends a
logical-path tombstone instead of deleting CAS objects or rewriting history.
Current-tree reconstruction applies the full ordered history, including those
tombstones.

Restoring an older workspace snapshot creates a **new** artifact version whose
manifest equals the historical target and whose parent is the current artifact
version. Paths that exist now but did not exist in the target manifest receive
tombstones, so restore is exact rather than additive.

Active write leases block a restore when it would change a leased non-mergeable
path. This prevents a restore from overwriting an agent's in-flight media edit.

## Next integrations

1. Persist workspace snapshots automatically when agent candidates complete.
2. Add background cleanup/reconciliation for expired or abandoned multipart staging sessions.
3. Add richer DOCX structure (headings, tables, comments) and richer spreadsheet formatting semantics.
4. Add visual pixel-diff/overlay derivatives for image review.
5. Add structured merge engines that can relax lease requirements safely.
