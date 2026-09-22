# Dev Plane TypeScript SDK

The TypeScript SDK exposes the Dev Plane API and a high-level direct multipart
artifact uploader for large workspace files.

## Direct multipart artifact upload

```ts
import {
  DevPlaneClient,
  type ArtifactUploadCheckpoint,
} from '@ai-cp/dev-plane-sdk';

const client = new DevPlaneClient({
  baseUrl: 'https://dev-plane.example.com',
  token,
});

let checkpoint: ArtifactUploadCheckpoint | undefined;

const artifact = await client.uploadWorkspaceArtifactMultipart(
  workspaceId,
  'renders/final.mp4',
  file,
  {
    concurrency: 4,
    checkpoint,
    onCheckpoint(next) {
      checkpoint = next;
      localStorage.setItem('artifact-upload', JSON.stringify(next));
    },
    onProgress(progress) {
      console.log(progress.phase, progress.processed_bytes, progress.total_bytes);
    },
  },
);
```

The uploader:

- computes SHA-256 incrementally in bounded chunks unless `sha256` is supplied,
- uploads parts directly to S3/R2 with bounded concurrency,
- retries transient part failures with exponential backoff,
- refreshes presigned URLs after a 403,
- persists caller-owned checkpoints after each completed part,
- resumes from an existing checkpoint without re-uploading finished parts,
- supports `AbortSignal` cancellation,
- completes with ordered multipart ETags,
- aborts the provider multipart session after terminal errors by default.

Set `abortOnError: false` when an application wants to preserve a failed
session for an explicit later retry. Process/browser crashes naturally leave
the SQL-backed session resumable until its server-side expiry.

If the caller already knows the file SHA-256, pass `sha256` to avoid rehashing
large files before upload.

## Browser CORS requirement

Browser uploads go directly to the object-store presigned URL. The bucket CORS
policy must:

1. allow the application origin,
2. allow `PUT`, and
3. expose the `ETag` response header.

The SDK treats a successful part upload without a visible ETag as an error,
because S3-compatible multipart completion requires the part ETags.
