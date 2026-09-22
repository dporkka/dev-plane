import type {
  ArtifactMetadata,
  BeginArtifactUploadRequest,
  BeginArtifactUploadResponse,
  CompleteArtifactUploadRequest,
  CompletedArtifactPart,
  PresignArtifactPartsRequest,
  PresignedArtifactPart,
} from './types.js';

export type ArtifactUploadSource = Blob | ArrayBuffer | Uint8Array;

export interface ArtifactUploadLease {
  token: string;
  generation: number;
}

export interface ArtifactUploadCheckpoint {
  upload_id: string;
  workspace_id: string;
  path: string;
  sha256: string;
  crc64nvme?: string;
  size_bytes: number;
  content_type: string;
  part_size_bytes: number;
  part_count: number;
  completed_parts: CompletedArtifactPart[];
}

export type ArtifactUploadPhase =
  | 'hashing'
  | 'initiating'
  | 'uploading'
  | 'completing'
  | 'completed'
  | 'aborting';

export interface ArtifactUploadProgress {
  phase: ArtifactUploadPhase;
  processed_bytes: number;
  total_bytes: number;
  completed_parts: number;
  part_count: number;
}

export interface ArtifactUploadOptions {
  contentType?: string;
  partSizeBytes?: number;
  concurrency?: number;
  maxRetries?: number;
  retryBaseDelayMs?: number;
  checksumChunkSizeBytes?: number;
  sha256?: string;
  crc64nvme?: string;
  lease?: ArtifactUploadLease;
  signal?: AbortSignal;
  checkpoint?: ArtifactUploadCheckpoint;
  onCheckpoint?: (checkpoint: ArtifactUploadCheckpoint) => void | Promise<void>;
  onProgress?: (progress: ArtifactUploadProgress) => void;
  abortOnError?: boolean;
}

export interface ArtifactUploadTransport {
  begin(
    workspaceId: string,
    payload: BeginArtifactUploadRequest,
    options?: { lease?: ArtifactUploadLease; signal?: AbortSignal },
  ): Promise<BeginArtifactUploadResponse>;
  presign(
    workspaceId: string,
    uploadId: string,
    payload: PresignArtifactPartsRequest,
    options?: { signal?: AbortSignal },
  ): Promise<{ parts: PresignedArtifactPart[] }>;
  complete(
    workspaceId: string,
    uploadId: string,
    payload: CompleteArtifactUploadRequest,
    options?: { lease?: ArtifactUploadLease; signal?: AbortSignal },
  ): Promise<ArtifactMetadata>;
  abort(
    workspaceId: string,
    uploadId: string,
    options?: { signal?: AbortSignal },
  ): Promise<void>;
}

const DEFAULT_CONCURRENCY = 4;
const DEFAULT_MAX_RETRIES = 4;
const DEFAULT_RETRY_BASE_DELAY_MS = 250;
const DEFAULT_CHECKSUM_CHUNK_SIZE = 8 * 1024 * 1024;
const PRESIGN_BATCH_SIZE = 32;

export async function uploadArtifactMultipart(
  transport: ArtifactUploadTransport,
  workspaceId: string,
  path: string,
  source: ArtifactUploadSource,
  options: ArtifactUploadOptions = {},
): Promise<ArtifactMetadata> {
  const size = sourceSize(source);
  if (size <= 0) {
    throw new Error('direct multipart upload requires a non-empty source');
  }

  const concurrency = boundedInteger(options.concurrency, DEFAULT_CONCURRENCY, 1, 16);
  const maxRetries = boundedInteger(options.maxRetries, DEFAULT_MAX_RETRIES, 0, 10);
  const retryBaseDelayMs = boundedInteger(
    options.retryBaseDelayMs,
    DEFAULT_RETRY_BASE_DELAY_MS,
    0,
    60_000,
  );
  const checksumChunkSize = boundedInteger(
    options.checksumChunkSizeBytes,
    DEFAULT_CHECKSUM_CHUNK_SIZE,
    64 * 1024,
    64 * 1024 * 1024,
  );
  const contentType =
    options.contentType ??
    (isBlob(source) && source.type ? source.type : 'application/octet-stream');

  throwIfAborted(options.signal);

  let sha256 = normalizeSha256(options.sha256 ?? options.checkpoint?.sha256);
  let crc64nvme = normalizeCRC64NVME(options.crc64nvme ?? options.checkpoint?.crc64nvme);
  const needsCRC64 = !options.checkpoint && !crc64nvme;
  if (!sha256 || needsCRC64) {
    reportProgress(options, {
      phase: 'hashing',
      processed_bytes: 0,
      total_bytes: size,
      completed_parts: 0,
      part_count: 0,
    });
    const checksums = await hashSourceIntegrity(
      source,
      checksumChunkSize,
      options.signal,
      (processed) => {
        reportProgress(options, {
          phase: 'hashing',
          processed_bytes: processed,
          total_bytes: size,
          completed_parts: 0,
          part_count: 0,
        });
      },
      !sha256,
      needsCRC64,
    );
    sha256 ??= checksums.sha256;
    crc64nvme ??= checksums.crc64nvme;
  }
  if (!sha256) {
    throw new Error('SHA-256 calculation did not produce a digest');
  }

  let session: BeginArtifactUploadResponse;
  let checkpoint = options.checkpoint;

  if (checkpoint) {
    validateCheckpoint(checkpoint, workspaceId, path, size, sha256, crc64nvme);
    session = {
      id: checkpoint.upload_id,
      workspace_id: checkpoint.workspace_id,
      path: checkpoint.path,
      digest: { algorithm: 'sha256', hex: checkpoint.sha256 },
      size_bytes: checkpoint.size_bytes,
      content_type: checkpoint.content_type,
      part_size_bytes: checkpoint.part_size_bytes,
      part_count: checkpoint.part_count,
      status: 'initiated',
      expires_at: '',
      verification_mode: 'stream_sha256',
      parts: [],
    };
  } else {
    reportProgress(options, {
      phase: 'initiating',
      processed_bytes: 0,
      total_bytes: size,
      completed_parts: 0,
      part_count: 0,
    });
    session = await transport.begin(
      workspaceId,
      {
        path,
        size_bytes: size,
        sha256,
        crc64nvme,
        content_type: contentType,
        part_size_bytes: options.partSizeBytes,
      },
      { lease: options.lease, signal: options.signal },
    );
    checkpoint = {
      upload_id: session.id,
      workspace_id: workspaceId,
      path,
      sha256,
      crc64nvme,
      size_bytes: size,
      content_type: session.content_type || contentType,
      part_size_bytes: session.part_size_bytes,
      part_count: session.part_count,
      completed_parts: [],
    };
    try {
      await saveCheckpoint(options, checkpoint);
    } catch (error) {
      if (options.abortOnError !== false) {
        try {
          await transport.abort(workspaceId, session.id);
        } catch {
          // Preserve the checkpoint persistence error.
        }
      }
      throw error;
    }
  }

  const completed = new Map<number, CompletedArtifactPart>();
  for (const part of checkpoint.completed_parts) {
    if (
      part.part_number < 1 ||
      part.part_number > checkpoint.part_count ||
      !part.etag
    ) {
      throw new Error('artifact upload checkpoint contains an invalid completed part');
    }
    completed.set(part.part_number, { ...part });
  }

  const initialParts = new Map<number, PresignedArtifactPart>();
  for (const part of session.parts ?? []) {
    initialParts.set(part.part_number, part);
  }
  const presignBatches = new Map<number, Promise<Map<number, PresignedArtifactPart>>>();

  let processedBytes = uploadedBytesForCompletedParts(
    completed,
    checkpoint.size_bytes,
    checkpoint.part_size_bytes,
  );
  reportProgress(options, {
    phase: 'uploading',
    processed_bytes: processedBytes,
    total_bytes: size,
    completed_parts: completed.size,
    part_count: checkpoint.part_count,
  });

  const pending: number[] = [];
  for (let partNumber = 1; partNumber <= checkpoint.part_count; partNumber++) {
    if (!completed.has(partNumber)) {
      pending.push(partNumber);
    }
  }

  let cursor = 0;
  let checkpointChain = Promise.resolve();

  const getPartUrl = async (
    partNumber: number,
    forceRefresh = false,
  ): Promise<PresignedArtifactPart> => {
    if (!forceRefresh) {
      const initial = initialParts.get(partNumber);
      if (initial) {
        return initial;
      }
    } else {
      initialParts.delete(partNumber);
    }

    const batchStart =
      Math.floor((partNumber - 1) / PRESIGN_BATCH_SIZE) * PRESIGN_BATCH_SIZE + 1;
    if (forceRefresh) {
      presignBatches.delete(batchStart);
    }

    let batchPromise = presignBatches.get(batchStart);
    if (!batchPromise) {
      const count = Math.min(PRESIGN_BATCH_SIZE, checkpoint!.part_count - batchStart + 1);
      batchPromise = transport
        .presign(
          workspaceId,
          checkpoint!.upload_id,
          { start_part: batchStart, count },
          { signal: options.signal },
        )
        .then((response) => {
          const parts = new Map<number, PresignedArtifactPart>();
          for (const part of response.parts) {
            parts.set(part.part_number, part);
          }
          return parts;
        });
      presignBatches.set(batchStart, batchPromise);
    }

    const part = (await batchPromise).get(partNumber);
    if (!part) {
      throw new Error(`server did not return a presigned URL for part ${partNumber}`);
    }
    return part;
  };

  const uploadOnePart = async (partNumber: number): Promise<void> => {
    const start = (partNumber - 1) * checkpoint!.part_size_bytes;
    const end = Math.min(start + checkpoint!.part_size_bytes, size);
    const body = sourceSlice(source, start, end);
    let forceRefresh = false;

    for (let attempt = 0; ; attempt++) {
      throwIfAborted(options.signal);
      const presigned = await getPartUrl(partNumber, forceRefresh);
      forceRefresh = false;

      let response: Response;
      try {
        response = await fetch(presigned.url, {
          method: 'PUT',
          body,
          signal: options.signal,
        });
      } catch (error) {
        if (isAbortError(error) || options.signal?.aborted) {
          throw abortError();
        }
        if (attempt >= maxRetries) {
          throw error;
        }
        await retryDelay(retryBaseDelayMs, attempt, options.signal);
        continue;
      }

      if (response.ok) {
        const etag = response.headers.get('ETag') ?? response.headers.get('etag');
        if (!etag) {
          throw new Error(
            'multipart upload response did not expose ETag; configure bucket CORS to expose the ETag header',
          );
        }
        completed.set(partNumber, { part_number: partNumber, etag });
        processedBytes += end - start;

        const snapshot: ArtifactUploadCheckpoint = {
          ...checkpoint!,
          completed_parts: sortedCompletedParts(completed),
        };
        checkpoint = snapshot;
        checkpointChain = checkpointChain.then(() => saveCheckpoint(options, snapshot));
        await checkpointChain;

        reportProgress(options, {
          phase: 'uploading',
          processed_bytes: processedBytes,
          total_bytes: size,
          completed_parts: completed.size,
          part_count: checkpoint.part_count,
        });
        return;
      }

      if (response.status === 403 && attempt < maxRetries) {
        forceRefresh = true;
        await retryDelay(retryBaseDelayMs, attempt, options.signal);
        continue;
      }

      if (isRetryableStatus(response.status) && attempt < maxRetries) {
        await retryDelay(retryBaseDelayMs, attempt, options.signal);
        continue;
      }

      const message = await safeResponseText(response);
      throw new Error(message || `multipart part ${partNumber} failed with HTTP ${response.status}`);
    }
  };

  const worker = async (): Promise<void> => {
    while (true) {
      const index = cursor++;
      if (index >= pending.length) {
        return;
      }
      await uploadOnePart(pending[index]);
    }
  };

  try {
    await Promise.all(Array.from({ length: Math.min(concurrency, pending.length) }, () => worker()));
    await checkpointChain;
    throwIfAborted(options.signal);

    reportProgress(options, {
      phase: 'completing',
      processed_bytes: size,
      total_bytes: size,
      completed_parts: completed.size,
      part_count: checkpoint.part_count,
    });

    const artifact = await transport.complete(
      workspaceId,
      checkpoint.upload_id,
      { parts: sortedCompletedParts(completed) },
      { lease: options.lease, signal: options.signal },
    );

    reportProgress(options, {
      phase: 'completed',
      processed_bytes: size,
      total_bytes: size,
      completed_parts: completed.size,
      part_count: checkpoint.part_count,
    });

    return artifact;
  } catch (error) {
    if (options.abortOnError !== false) {
      reportProgress(options, {
        phase: 'aborting',
        processed_bytes: processedBytes,
        total_bytes: size,
        completed_parts: completed.size,
        part_count: checkpoint.part_count,
      });
      try {
        await transport.abort(workspaceId, checkpoint.upload_id);
      } catch {
        // Preserve the original upload error. Server-side expiry/cleanup remains
        // the fallback when an abort request itself cannot be delivered.
      }
    }
    throw error;
  }
}

function sourceSize(source: ArtifactUploadSource): number {
  if (isBlob(source)) {
    return source.size;
  }
  return source.byteLength;
}

function sourceSlice(
  source: ArtifactUploadSource,
  start: number,
  end: number,
): Blob | ArrayBuffer {
  if (isBlob(source)) {
    return source.slice(start, end);
  }
  const bytes =
    source instanceof Uint8Array
      ? source.subarray(start, end)
      : new Uint8Array(source, start, end - start);
  const copy = new Uint8Array(bytes.byteLength);
  copy.set(bytes);
  return copy.buffer;
}

async function sourceSliceBytes(
  source: ArtifactUploadSource,
  start: number,
  end: number,
): Promise<Uint8Array> {
  const slice = sourceSlice(source, start, end);
  if (isBlob(slice)) {
    return new Uint8Array(await slice.arrayBuffer());
  }
  return new Uint8Array(slice);
}

function isBlob(value: unknown): value is Blob {
  return typeof Blob !== 'undefined' && value instanceof Blob;
}

function boundedInteger(
  value: number | undefined,
  fallback: number,
  min: number,
  max: number,
): number {
  const actual = value ?? fallback;
  if (!Number.isInteger(actual) || actual < min || actual > max) {
    throw new Error(`expected integer between ${min} and ${max}, got ${actual}`);
  }
  return actual;
}

function normalizeSha256(value: string | undefined): string | undefined {
  if (!value) {
    return undefined;
  }
  const normalized = value.trim().toLowerCase().replace(/^sha256:/, '');
  if (!/^[0-9a-f]{64}$/.test(normalized)) {
    throw new Error('sha256 must be a 64-character hexadecimal digest');
  }
  return normalized;
}

function normalizeCRC64NVME(value: string | undefined): string | undefined {
  if (!value) {
    return undefined;
  }
  const normalized = value.trim();
  if (!/^[A-Za-z0-9+/]{11}=$/.test(normalized)) {
    throw new Error('crc64nvme must be a base64-encoded 8-byte checksum');
  }
  return normalized;
}

function validateCheckpoint(
  checkpoint: ArtifactUploadCheckpoint,
  workspaceId: string,
  path: string,
  size: number,
  sha256: string,
  crc64nvme?: string,
): void {
  if (
    checkpoint.workspace_id !== workspaceId ||
    checkpoint.path !== path ||
    checkpoint.size_bytes !== size ||
    checkpoint.sha256 !== sha256 ||
    (checkpoint.crc64nvme !== undefined &&
      crc64nvme !== undefined &&
      checkpoint.crc64nvme !== crc64nvme)
  ) {
    throw new Error('artifact upload checkpoint does not match the requested upload');
  }
  if (checkpoint.part_size_bytes <= 0 || checkpoint.part_count <= 0) {
    throw new Error('artifact upload checkpoint has invalid multipart geometry');
  }
  const expectedParts = Math.ceil(size / checkpoint.part_size_bytes);
  if (checkpoint.part_count !== expectedParts) {
    throw new Error('artifact upload checkpoint part count does not match its part size');
  }
}

function sortedCompletedParts(
  completed: Map<number, CompletedArtifactPart>,
): CompletedArtifactPart[] {
  return [...completed.values()].sort((a, b) => a.part_number - b.part_number);
}

function uploadedBytesForCompletedParts(
  completed: Map<number, CompletedArtifactPart>,
  size: number,
  partSize: number,
): number {
  let total = 0;
  for (const number of completed.keys()) {
    const start = (number - 1) * partSize;
    total += Math.max(0, Math.min(partSize, size - start));
  }
  return total;
}

async function saveCheckpoint(
  options: ArtifactUploadOptions,
  checkpoint: ArtifactUploadCheckpoint,
): Promise<void> {
  if (!options.onCheckpoint) {
    return;
  }
  await options.onCheckpoint({
    ...checkpoint,
    completed_parts: checkpoint.completed_parts.map((part) => ({ ...part })),
  });
}

function reportProgress(
  options: ArtifactUploadOptions,
  progress: ArtifactUploadProgress,
): void {
  options.onProgress?.(progress);
}

function isRetryableStatus(status: number): boolean {
  return status === 408 || status === 425 || status === 429 || status >= 500;
}

async function safeResponseText(response: Response): Promise<string> {
  try {
    return (await response.text()).trim();
  } catch {
    return '';
  }
}

async function retryDelay(
  baseDelayMs: number,
  attempt: number,
  signal?: AbortSignal,
): Promise<void> {
  if (baseDelayMs === 0) {
    throwIfAborted(signal);
    return;
  }
  const delay = Math.min(baseDelayMs * 2 ** attempt, 30_000);
  await new Promise<void>((resolve, reject) => {
    let settled = false;
    const finish = (callback: () => void) => {
      if (settled) {
        return;
      }
      settled = true;
      if (signal) {
        signal.removeEventListener('abort', onAbort);
      }
      callback();
    };
    const timer = setTimeout(() => finish(resolve), delay);
    const onAbort = () => {
      clearTimeout(timer);
      finish(() => reject(abortError()));
    };
    if (!signal) {
      return;
    }
    if (signal.aborted) {
      onAbort();
      return;
    }
    signal.addEventListener('abort', onAbort, { once: true });
  });
}

function throwIfAborted(signal?: AbortSignal): void {
  if (signal?.aborted) {
    throw abortError();
  }
}

function abortError(): Error {
  if (typeof DOMException !== 'undefined') {
    return new DOMException('The operation was aborted', 'AbortError');
  }
  const error = new Error('The operation was aborted');
  error.name = 'AbortError';
  return error;
}

function isAbortError(error: unknown): boolean {
  return error instanceof Error && error.name === 'AbortError';
}

export async function hashSourceSHA256(
  source: ArtifactUploadSource,
  chunkSize = DEFAULT_CHECKSUM_CHUNK_SIZE,
  signal?: AbortSignal,
  onProgress?: (processedBytes: number) => void,
): Promise<string> {
  const result = await hashSourceIntegrity(
    source,
    chunkSize,
    signal,
    onProgress,
    true,
    false,
  );
  if (!result.sha256) {
    throw new Error('SHA-256 calculation did not produce a digest');
  }
  return result.sha256;
}

export async function hashSourceCRC64NVME(
  source: ArtifactUploadSource,
  chunkSize = DEFAULT_CHECKSUM_CHUNK_SIZE,
  signal?: AbortSignal,
  onProgress?: (processedBytes: number) => void,
): Promise<string> {
  const result = await hashSourceIntegrity(
    source,
    chunkSize,
    signal,
    onProgress,
    false,
    true,
  );
  if (!result.crc64nvme) {
    throw new Error('CRC64/NVME calculation did not produce a checksum');
  }
  return result.crc64nvme;
}

async function hashSourceIntegrity(
  source: ArtifactUploadSource,
  chunkSize: number,
  signal: AbortSignal | undefined,
  onProgress: ((processedBytes: number) => void) | undefined,
  includeSHA256: boolean,
  includeCRC64NVME: boolean,
): Promise<{ sha256?: string; crc64nvme?: string }> {
  if (!Number.isInteger(chunkSize) || chunkSize <= 0) {
    throw new Error('checksum chunk size must be a positive integer');
  }
  const size = sourceSize(source);
  const sha256 = includeSHA256 ? new IncrementalSHA256() : undefined;
  const crc64nvme = includeCRC64NVME ? new IncrementalCRC64NVME() : undefined;
  for (let offset = 0; offset < size; offset += chunkSize) {
    throwIfAborted(signal);
    const end = Math.min(offset + chunkSize, size);
    const bytes = await sourceSliceBytes(source, offset, end);
    sha256?.update(bytes);
    crc64nvme?.update(bytes);
    onProgress?.(end);
  }
  return {
    sha256: sha256?.hex(),
    crc64nvme: crc64nvme?.base64(),
  };
}

const CRC64_NVME_MASK = 0xffffffffffffffffn;
const CRC64_NVME_REVERSED_POLYNOMIAL = 0x9a6c9329ac4bc9b5n;
const CRC64_NVME_TABLE = buildCRC64NVMETable();

function buildCRC64NVMETable(): readonly bigint[] {
  const table: bigint[] = [];
  for (let i = 0; i < 256; i++) {
    let crc = BigInt(i);
    for (let bit = 0; bit < 8; bit++) {
      crc =
        (crc & 1n) === 1n
          ? (crc >> 1n) ^ CRC64_NVME_REVERSED_POLYNOMIAL
          : crc >> 1n;
    }
    table.push(crc & CRC64_NVME_MASK);
  }
  return table;
}

class IncrementalCRC64NVME {
  private value = 0n;

  update(data: Uint8Array): void {
    let crc = (~this.value) & CRC64_NVME_MASK;
    for (const byte of data) {
      const index = Number((crc ^ BigInt(byte)) & 0xffn);
      crc = CRC64_NVME_TABLE[index] ^ (crc >> 8n);
    }
    this.value = (~crc) & CRC64_NVME_MASK;
  }

  base64(): string {
    const bytes = new Uint8Array(8);
    let value = this.value;
    for (let i = 7; i >= 0; i--) {
      bytes[i] = Number(value & 0xffn);
      value >>= 8n;
    }
    return base64Encode(bytes);
  }
}

function base64Encode(bytes: Uint8Array): string {
  const alphabet =
    'ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789+/';
  let output = '';
  for (let offset = 0; offset < bytes.length; offset += 3) {
    const remaining = bytes.length - offset;
    const a = bytes[offset];
    const b = remaining > 1 ? bytes[offset + 1] : 0;
    const d = remaining > 2 ? bytes[offset + 2] : 0;
    const word = (a << 16) | (b << 8) | d;
    output += alphabet[(word >>> 18) & 63];
    output += alphabet[(word >>> 12) & 63];
    output += remaining > 1 ? alphabet[(word >>> 6) & 63] : '=';
    output += remaining > 2 ? alphabet[word & 63] : '=';
  }
  return output;
}

class IncrementalSHA256 {
  private readonly state = new Uint32Array([
    0x6a09e667,
    0xbb67ae85,
    0x3c6ef372,
    0xa54ff53a,
    0x510e527f,
    0x9b05688c,
    0x1f83d9ab,
    0x5be0cd19,
  ]);
  private readonly buffer = new Uint8Array(64);
  private bufferLength = 0;
  private bytesHashed = 0;
  private finished = false;

  update(data: Uint8Array): void {
    if (this.finished) {
      throw new Error('SHA-256 digest already finalized');
    }
    this.bytesHashed += data.length;
    let offset = 0;

    if (this.bufferLength > 0) {
      const take = Math.min(64 - this.bufferLength, data.length);
      this.buffer.set(data.subarray(0, take), this.bufferLength);
      this.bufferLength += take;
      offset += take;
      if (this.bufferLength === 64) {
        this.transform(this.buffer);
        this.bufferLength = 0;
      }
    }

    while (offset + 64 <= data.length) {
      this.transform(data.subarray(offset, offset + 64));
      offset += 64;
    }

    if (offset < data.length) {
      const remaining = data.subarray(offset);
      this.buffer.set(remaining, 0);
      this.bufferLength = remaining.length;
    }
  }

  hex(): string {
    if (!this.finished) {
      this.finish();
    }
    let output = '';
    for (const word of this.state) {
      output += word.toString(16).padStart(8, '0');
    }
    return output;
  }

  private finish(): void {
    const bitLength = this.bytesHashed * 8;
    const high = Math.floor(bitLength / 0x100000000);
    const low = bitLength >>> 0;

    this.buffer[this.bufferLength++] = 0x80;
    if (this.bufferLength > 56) {
      this.buffer.fill(0, this.bufferLength);
      this.transform(this.buffer);
      this.bufferLength = 0;
    }
    this.buffer.fill(0, this.bufferLength, 56);

    const view = new DataView(this.buffer.buffer);
    view.setUint32(56, high, false);
    view.setUint32(60, low, false);
    this.transform(this.buffer);
    this.bufferLength = 0;
    this.finished = true;
  }

  private transform(block: Uint8Array): void {
    const words = new Uint32Array(64);
    const view = new DataView(block.buffer, block.byteOffset, block.byteLength);
    for (let i = 0; i < 16; i++) {
      words[i] = view.getUint32(i * 4, false);
    }
    for (let i = 16; i < 64; i++) {
      const x = words[i - 15];
      const y = words[i - 2];
      const s0 = rotr(x, 7) ^ rotr(x, 18) ^ (x >>> 3);
      const s1 = rotr(y, 17) ^ rotr(y, 19) ^ (y >>> 10);
      words[i] = (words[i - 16] + s0 + words[i - 7] + s1) >>> 0;
    }

    let a = this.state[0];
    let b = this.state[1];
    let c = this.state[2];
    let d = this.state[3];
    let e = this.state[4];
    let f = this.state[5];
    let g = this.state[6];
    let h = this.state[7];

    for (let i = 0; i < 64; i++) {
      const s1 = rotr(e, 6) ^ rotr(e, 11) ^ rotr(e, 25);
      const choice = (e & f) ^ (~e & g);
      const temp1 = (h + s1 + choice + SHA256_K[i] + words[i]) >>> 0;
      const s0 = rotr(a, 2) ^ rotr(a, 13) ^ rotr(a, 22);
      const majority = (a & b) ^ (a & c) ^ (b & c);
      const temp2 = (s0 + majority) >>> 0;

      h = g;
      g = f;
      f = e;
      e = (d + temp1) >>> 0;
      d = c;
      c = b;
      b = a;
      a = (temp1 + temp2) >>> 0;
    }

    this.state[0] = (this.state[0] + a) >>> 0;
    this.state[1] = (this.state[1] + b) >>> 0;
    this.state[2] = (this.state[2] + c) >>> 0;
    this.state[3] = (this.state[3] + d) >>> 0;
    this.state[4] = (this.state[4] + e) >>> 0;
    this.state[5] = (this.state[5] + f) >>> 0;
    this.state[6] = (this.state[6] + g) >>> 0;
    this.state[7] = (this.state[7] + h) >>> 0;
  }
}

function rotr(value: number, bits: number): number {
  return (value >>> bits) | (value << (32 - bits));
}

const SHA256_K = new Uint32Array([
  0x428a2f98, 0x71374491, 0xb5c0fbcf, 0xe9b5dba5, 0x3956c25b, 0x59f111f1,
  0x923f82a4, 0xab1c5ed5, 0xd807aa98, 0x12835b01, 0x243185be, 0x550c7dc3,
  0x72be5d74, 0x80deb1fe, 0x9bdc06a7, 0xc19bf174, 0xe49b69c1, 0xefbe4786,
  0x0fc19dc6, 0x240ca1cc, 0x2de92c6f, 0x4a7484aa, 0x5cb0a9dc, 0x76f988da,
  0x983e5152, 0xa831c66d, 0xb00327c8, 0xbf597fc7, 0xc6e00bf3, 0xd5a79147,
  0x06ca6351, 0x14292967, 0x27b70a85, 0x2e1b2138, 0x4d2c6dfc, 0x53380d13,
  0x650a7354, 0x766a0abb, 0x81c2c92e, 0x92722c85, 0xa2bfe8a1, 0xa81a664b,
  0xc24b8b70, 0xc76c51a3, 0xd192e819, 0xd6990624, 0xf40e3585, 0x106aa070,
  0x19a4c116, 0x1e376c08, 0x2748774c, 0x34b0bcb5, 0x391c0cb3, 0x4ed8aa4a,
  0x5b9cca4f, 0x682e6ff3, 0x748f82ee, 0x78a5636f, 0x84c87814, 0x8cc70208,
  0x90befffa, 0xa4506ceb, 0xbef9a3f7, 0xc67178f2,
]);
