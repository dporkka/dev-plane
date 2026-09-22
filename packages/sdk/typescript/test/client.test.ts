import { describe, test, before, after } from 'node:test';
import assert from 'node:assert/strict';
import {
  DevPlaneClient,
  hashSourceCRC64NVME,
  hashSourceSHA256,
  uploadArtifactMultipart,
  type ArtifactUploadTransport,
} from '../src/index.js';

interface MockState {
  input: string;
  init?: RequestInit;
}

describe('DevPlaneClient', () => {
  const originalFetch = globalThis.fetch;
  let state: MockState = { input: '' };

  const mockFetch = async (
    input: RequestInfo | URL,
    init?: RequestInit,
  ): Promise<Response> => {
    state = {
      input: typeof input === 'string' ? input : input.toString(),
      init,
    };

    if (state.input.endsWith('/artifacts/art-1/content')) {
      return new Response(new Blob(['artifact-body']), {
        status: 200,
        headers: { 'Content-Type': 'application/octet-stream' },
      });
    }

    return new Response(JSON.stringify({ ok: true }), {
      status: 200,
      headers: { 'Content-Type': 'application/json' },
    });
  };

  before(() => {
    globalThis.fetch = mockFetch as typeof fetch;
  });

  after(() => {
    globalThis.fetch = originalFetch;
  });

  test('sends the bearer token and returns JSON', async () => {
    const client = new DevPlaneClient({ baseUrl: 'http://api.test', token: 'token-123' });
    const result = (await client.listOrganizations()) as unknown as { ok: boolean };

    assert.equal(state.input, 'http://api.test/api/v1/organizations');
    assert.equal((state.init?.headers as Record<string, string>)?.Authorization, 'Bearer token-123');
    assert.equal(result.ok, true);
  });

  test('builds task list query strings', async () => {
    const client = new DevPlaneClient({ baseUrl: 'http://api.test' });
    await client.listTasks('proj-1', { status: 'running' });

    assert.equal(state.input, 'http://api.test/api/v1/projects/proj-1/tasks?status=running');
  });

  test('serializes request bodies', async () => {
    const client = new DevPlaneClient({ baseUrl: 'http://api.test' });
    await client.createTask('proj-1', {
      repository_id: 'repo-1',
      title: 'Add SDK coverage',
    });

    assert.equal(state.input, 'http://api.test/api/v1/projects/proj-1/tasks');
    assert.equal(state.init?.method, 'POST');
    assert.deepEqual(JSON.parse(state.init?.body as string), {
      repository_id: 'repo-1',
      title: 'Add SDK coverage',
    });
  });

  test('returns artifact responses as Blobs', async () => {
    const client = new DevPlaneClient({ baseUrl: 'http://api.test' });
    const blob = await client.getArtifact('art-1');

    assert.equal(state.input, 'http://api.test/api/v1/artifacts/art-1/content');
    assert.equal(blob.size, 'artifact-body'.length);
    assert.equal(await blob.text(), 'artifact-body');
  });

  test('lists current workspace artifacts', async () => {
    const client = new DevPlaneClient({ baseUrl: 'http://api.test' });
    await client.listWorkspaceArtifacts('ws-1');

    assert.equal(state.input, 'http://api.test/api/v1/workspaces/ws-1/artifacts');
  });

  test('restores a historical artifact snapshot', async () => {
    const client = new DevPlaneClient({ baseUrl: 'http://api.test' });
    await client.restoreWorkspaceArtifacts('ws-1', 'snap-7');

    assert.equal(
      state.input,
      'http://api.test/api/v1/workspaces/ws-1/snapshots/snap-7/restore-artifacts',
    );
    assert.equal(state.init?.method, 'POST');
  });

  test('begins direct artifact uploads with lease headers', async () => {
    const client = new DevPlaneClient({ baseUrl: 'http://api.test', token: 'token-123' });
    await client.beginArtifactUpload(
      'ws-1',
      {
        path: 'movie.mp4',
        size_bytes: 128,
        sha256: 'ab'.repeat(32),
        content_type: 'video/mp4',
      },
      {
        lease: { token: 'lease-token', generation: 7 },
      },
    );

    assert.equal(
      state.input,
      'http://api.test/api/v1/workspaces/ws-1/artifact-uploads',
    );
    assert.equal(state.init?.method, 'POST');
    const headers = state.init?.headers as Record<string, string>;
    assert.equal(headers.Authorization, 'Bearer token-123');
    assert.equal(headers['X-Artifact-Lease-Token'], 'lease-token');
    assert.equal(headers['X-Artifact-Lease-Generation'], '7');
    assert.deepEqual(JSON.parse(state.init?.body as string), {
      path: 'movie.mp4',
      size_bytes: 128,
      sha256: 'ab'.repeat(32),
      content_type: 'video/mp4',
    });
  });
});


describe('artifact multipart uploader', () => {
  test('streams SHA-256 without materializing the whole Blob', async () => {
    const digest = await hashSourceSHA256(new Blob(['abc']), 2);
    assert.equal(
      digest,
      'ba7816bf8f01cfea414140de5dae2223b00361a396177a9cb410ff61f20015ad',
    );
  });

  test('streams CRC64/NVME using the standard 123456789 vector', async () => {
    const checksum = await hashSourceCRC64NVME(new Blob(['123456789']), 3);
    assert.equal(checksum, 'rosUhgp5mIg=');
  });

  test('uploads parts with retries, checkpoints, and ordered completion', async () => {
    const originalFetch = globalThis.fetch;
    const uploadedBodies = new Map<number, string>();
    const attempts = new Map<number, number>();
    let completedParts: Array<{ part_number: number; etag: string }> = [];
    let abortCalls = 0;
    const checkpoints: number[][] = [];

    globalThis.fetch = (async (input: RequestInfo | URL, init?: RequestInit) => {
      const url = typeof input === 'string' ? input : input.toString();
      const match = /part-(\d+)$/.exec(url);
      assert.ok(match, `unexpected upload URL: ${url}`);
      const part = Number(match[1]);
      attempts.set(part, (attempts.get(part) ?? 0) + 1);

      if (part === 2 && attempts.get(part) === 1) {
        return new Response('retry me', { status: 500 });
      }

      const body = init?.body;
      assert.ok(body instanceof Blob || body instanceof ArrayBuffer);
      const text =
        body instanceof Blob
          ? await body.text()
          : new TextDecoder().decode(new Uint8Array(body));
      uploadedBodies.set(part, text);

      return new Response(null, {
        status: 200,
        headers: { ETag: `"etag-${part}"` },
      });
    }) as typeof fetch;

    const transport: ArtifactUploadTransport = {
      async begin(workspaceId, payload) {
        assert.equal(workspaceId, 'ws-1');
        assert.equal(payload.path, 'movie.bin');
        return {
          id: 'upload-1',
          workspace_id: workspaceId,
          path: payload.path,
          digest: { algorithm: 'sha256', hex: payload.sha256 },
          size_bytes: payload.size_bytes,
          content_type: payload.content_type ?? 'application/octet-stream',
          part_size_bytes: 4,
          part_count: 3,
          status: 'initiated',
          verification_mode: 'stream_sha256',
          expires_at: new Date(Date.now() + 60_000).toISOString(),
          parts: [
            { part_number: 1, url: 'https://upload.test/part-1', expires_at: '' },
            { part_number: 2, url: 'https://upload.test/part-2', expires_at: '' },
            { part_number: 3, url: 'https://upload.test/part-3', expires_at: '' },
          ],
        };
      },
      async presign() {
        throw new Error('initial URLs should cover this test upload');
      },
      async complete(_workspaceId, _uploadId, payload) {
        completedParts = payload.parts;
        return {
          id: 'artifact-1',
          organization_id: 'org-1',
          workspace_id: 'ws-1',
          artifact_type: 'workspace_file',
          file_name: 'movie.bin',
          logical_path: 'movie.bin',
          kind: 'binary',
          size_bytes: 10,
          digest: { algorithm: 'sha256', hex: '00'.repeat(32) },
          created_at: new Date().toISOString(),
        };
      },
      async abort() {
        abortCalls++;
      },
    };

    try {
      const artifact = await uploadArtifactMultipart(
        transport,
        'ws-1',
        'movie.bin',
        new Blob(['abcdefghij']),
        {
          partSizeBytes: 4,
          concurrency: 2,
          maxRetries: 2,
          retryBaseDelayMs: 0,
          sha256: '01'.repeat(32),
          onCheckpoint(checkpoint) {
            checkpoints.push(checkpoint.completed_parts.map((part) => part.part_number));
          },
        },
      );

      assert.equal(artifact.id, 'artifact-1');
      assert.equal(uploadedBodies.get(1), 'abcd');
      assert.equal(uploadedBodies.get(2), 'efgh');
      assert.equal(uploadedBodies.get(3), 'ij');
      assert.equal(attempts.get(2), 2);
      assert.deepEqual(
        completedParts.map((part) => part.part_number),
        [1, 2, 3],
      );
      assert.ok(checkpoints.some((parts) => parts.length === 3));
      assert.equal(abortCalls, 0);
    } finally {
      globalThis.fetch = originalFetch;
    }
  });

  test('resumes from completed checkpoint parts', async () => {
    const originalFetch = globalThis.fetch;
    const uploaded: number[] = [];
    let completedParts: Array<{ part_number: number; etag: string }> = [];

    globalThis.fetch = (async (input: RequestInfo | URL) => {
      const url = typeof input === 'string' ? input : input.toString();
      const part = Number(url.split('-').pop());
      uploaded.push(part);
      return new Response(null, {
        status: 200,
        headers: { ETag: `"etag-${part}"` },
      });
    }) as typeof fetch;

    const transport: ArtifactUploadTransport = {
      async begin() {
        throw new Error('resume must not initiate a second multipart upload');
      },
      async presign(_workspaceId, _uploadId, payload) {
        const parts = [];
        for (let part = payload.start_part; part < payload.start_part + payload.count; part++) {
          if (part <= 3) {
            parts.push({
              part_number: part,
              url: `https://upload.test/part-${part}`,
              expires_at: '',
            });
          }
        }
        return { parts };
      },
      async complete(_workspaceId, _uploadId, payload) {
        completedParts = payload.parts;
        return {
          id: 'artifact-resumed',
          organization_id: 'org-1',
          workspace_id: 'ws-1',
          artifact_type: 'workspace_file',
          file_name: 'resume.bin',
          logical_path: 'resume.bin',
          kind: 'binary',
          size_bytes: 10,
          digest: { algorithm: 'sha256', hex: '02'.repeat(32) },
          created_at: new Date().toISOString(),
        };
      },
      async abort() {},
    };

    try {
      await uploadArtifactMultipart(
        transport,
        'ws-1',
        'resume.bin',
        new Blob(['abcdefghij']),
        {
          sha256: '02'.repeat(32),
          concurrency: 2,
          retryBaseDelayMs: 0,
          checkpoint: {
            upload_id: 'upload-existing',
            workspace_id: 'ws-1',
            path: 'resume.bin',
            sha256: '02'.repeat(32),
            size_bytes: 10,
            content_type: 'application/octet-stream',
            part_size_bytes: 4,
            part_count: 3,
            completed_parts: [{ part_number: 1, etag: '"etag-1"' }],
          },
        },
      );

      assert.deepEqual(uploaded.sort(), [2, 3]);
      assert.deepEqual(
        completedParts.map((part) => part.part_number),
        [1, 2, 3],
      );
    } finally {
      globalThis.fetch = originalFetch;
    }
  });

  test('aborts the multipart session after an unrecoverable part error', async () => {
    const originalFetch = globalThis.fetch;
    let abortCalls = 0;

    globalThis.fetch = (async () => new Response('bad request', { status: 400 })) as typeof fetch;

    const transport: ArtifactUploadTransport = {
      async begin(workspaceId, payload) {
        return {
          id: 'upload-fail',
          workspace_id: workspaceId,
          path: payload.path,
          digest: { algorithm: 'sha256', hex: payload.sha256 },
          size_bytes: payload.size_bytes,
          content_type: 'application/octet-stream',
          part_size_bytes: payload.size_bytes,
          part_count: 1,
          status: 'initiated',
          verification_mode: 'stream_sha256',
          expires_at: '',
          parts: [{ part_number: 1, url: 'https://upload.test/part-1', expires_at: '' }],
        };
      },
      async presign() {
        throw new Error('unexpected presign');
      },
      async complete() {
        throw new Error('completion should not run');
      },
      async abort() {
        abortCalls++;
      },
    };

    try {
      await assert.rejects(
        uploadArtifactMultipart(
          transport,
          'ws-1',
          'broken.bin',
          new Blob(['broken']),
          {
            sha256: '03'.repeat(32),
            maxRetries: 0,
            retryBaseDelayMs: 0,
          },
        ),
        /bad request/,
      );
      assert.equal(abortCalls, 1);
    } finally {
      globalThis.fetch = originalFetch;
    }
  });
});
