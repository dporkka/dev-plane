import { describe, test, before, after } from 'node:test';
import assert from 'node:assert/strict';
import { DevPlaneClient } from '../src/index.js';

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

    if (state.input.endsWith('/artifacts/art-1')) {
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

  test('encodes external PR triage filters and retains bearer auth', async () => {
    const client = new DevPlaneClient({ baseUrl: 'http://api.test', token: 'test-token' });
    await client.listExternalPRTriage('org-1', { bucket: 'needs-repair', repo: 'my org/repo', offset: 50, limit: 50 });
    const requested = new URL(state.input);
    assert.equal(requested.pathname, '/api/v1/organizations/org-1/pr-triage');
    assert.equal(requested.searchParams.get('bucket'), 'needs-repair');
    assert.equal(requested.searchParams.get('repo'), 'my org/repo');
    assert.equal(requested.searchParams.get('offset'), '50');
    assert.equal(requested.searchParams.get('limit'), '50');
    assert.equal((state.init?.headers as Record<string, string>)?.Authorization, 'Bearer test-token');
  });

  test('posts bounded scanner snapshots without changing GitHub', async () => {
    const client = new DevPlaneClient({ baseUrl: 'http://api.test' });
    const snapshot = { generated_at: '2026-11-21T12:00:00Z', decisions: [] };
    await client.importExternalPRTriage('org-1', snapshot);
    assert.equal(state.input, 'http://api.test/api/v1/organizations/org-1/pr-triage/import');
    assert.equal(state.init?.method, 'POST');
    assert.deepEqual(JSON.parse(state.init?.body as string), snapshot);
  });

  test('returns artifact responses as Blobs', async () => {
    const client = new DevPlaneClient({ baseUrl: 'http://api.test' });
    const blob = await client.getArtifact('art-1');

    assert.equal(state.input, 'http://api.test/api/v1/artifacts/art-1');
    assert.equal(blob.size, 'artifact-body'.length);
    assert.equal(await blob.text(), 'artifact-body');
  });
});
