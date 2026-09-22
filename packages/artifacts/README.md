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

## Next integrations

1. Wire artifact manifests into VCS snapshots/workspace metadata.
2. Add an R2/S3 `BlobStore`.
3. Add large-object chunk manifests and content-defined chunking.
4. Add MIME-aware document/PDF/image adapters.
5. Add short-lived write leases for non-mergeable formats.
