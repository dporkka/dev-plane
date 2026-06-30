#!/usr/bin/env python3
"""
wrpc_transport.py -- Development helper for invoking wRPC methods over NATS.

This script provides:
  - An async NATS connection helper with bounded reconnect backoff.
  - wRPC-style framed chunk encoding/decoding.
  - ACK-first chunk streams: an immediate ACK frame, then DATA/QUERY chunks,
    terminated by a strict 0-byte frame.
  - Example invocations for agent prompt handling and Nulang compilation.

Install dependencies:
    pip install nats-py

Example:
    python wrpc_transport.py \
        --nats nats://100.64.0.10:4222 \
        --subject agents.prompt.coder.team-alpha.session-001 \
        prompt --payload '{"task":"hello"}'
"""

from __future__ import annotations

import argparse
import asyncio
import json
import logging
import struct
import sys
from dataclasses import dataclass, field
from enum import Enum
from typing import Any, Callable

import nats
from nats.aio.client import Client as NatsClient
from nats.aio.msg import Msg
from nats.aio.subscription import Subscription

logging.basicConfig(level=logging.INFO, format="%(asctime)s %(levelname)s %(message)s")
logger = logging.getLogger("wrpc")


# ---------------------------------------------------------------------------
# Framing primitives
# ---------------------------------------------------------------------------
TERMINAL: bytes = b"\x00"
CHUNK_HEADER_FMT = ">I"  # big-endian 4-byte length prefix


class ChunkType(Enum):
    """Typed chunk kinds used in response pipes."""

    ACK = "ACK"
    DATA = "DATA"
    QUERY = "QUERY"


@dataclass
class ChunkFrame:
    """A single typed frame in a chunk stream."""

    kind: ChunkType
    seq: int
    payload: bytes

    def encode(self) -> bytes:
        """Encode the frame as a length-prefixed byte string."""
        header = f"{self.kind.value}:{self.seq}:".encode("utf-8")
        body = header + self.payload
        return struct.pack(CHUNK_HEADER_FMT, len(body)) + body

    @staticmethod
    def decode(data: bytes) -> "ChunkFrame":
        """Decode a single length-prefixed frame from raw bytes."""
        if len(data) < 4:
            raise ValueError("Truncated chunk header")
        (length,) = struct.unpack(CHUNK_HEADER_FMT, data[:4])
        body = data[4 : 4 + length]
        if len(body) != length:
            raise ValueError("Truncated chunk body")

        prefix, sep, rest = body.partition(b":")
        if not sep:
            raise ValueError("Malformed chunk frame: missing kind")
        try:
            kind = ChunkType(prefix.decode("utf-8"))
        except (ValueError, UnicodeDecodeError) as exc:
            raise ValueError(f"Unknown chunk kind: {prefix!r}") from exc

        seq_str, sep2, payload = rest.partition(b":")
        if not sep2:
            raise ValueError("Malformed chunk frame: missing sequence")
        return ChunkFrame(kind=kind, seq=int(seq_str), payload=payload)


def encode_chunk_stream(
    ack_payload: bytes = b"",
    data_chunks: list[bytes] | None = None,
    query_chunks: list[bytes] | None = None,
) -> bytes:
    """
    Encode a complete ACK-first chunk stream.

    The stream is: ACK:0, DATA:1..N, QUERY:N+1..M, terminal 0x00 byte.
    """
    out = bytearray()
    out.extend(ChunkFrame(ChunkType.ACK, seq=0, payload=ack_payload).encode())

    seq = 1
    for chunk in data_chunks or []:
        out.extend(ChunkFrame(ChunkType.DATA, seq=seq, payload=chunk).encode())
        seq += 1
    for chunk in query_chunks or []:
        out.extend(ChunkFrame(ChunkType.QUERY, seq=seq, payload=chunk).encode())
        seq += 1

    out.extend(TERMINAL)
    return bytes(out)


def decode_chunk_stream(data: bytes) -> list[ChunkFrame]:
    """
    Decode a chunk stream until the terminal 0-byte frame is seen.

    Returns all non-terminal frames.  Raises ValueError on malformed input.
    """
    frames: list[ChunkFrame] = []
    i = 0
    while i < len(data):
        if data[i : i + 1] == TERMINAL:
            if i != len(data) - 1:
                raise ValueError("Terminal frame must be the final byte")
            return frames
        if i + 4 > len(data):
            raise ValueError("Truncated chunk header")
        (length,) = struct.unpack(CHUNK_HEADER_FMT, data[i : i + 4])
        frame_end = i + 4 + length
        if frame_end > len(data):
            raise ValueError("Truncated chunk body")
        frames.append(ChunkFrame.decode(data[i:frame_end]))
        i = frame_end
    raise ValueError("Missing terminal signal")


def decode_chunk_stream_payloads(data: bytes) -> dict[ChunkType, list[bytes]]:
    """Convenience helper that groups decoded payloads by chunk type."""
    grouped: dict[ChunkType, list[bytes]] = {
        ChunkType.ACK: [],
        ChunkType.DATA: [],
        ChunkType.QUERY: [],
    }
    for frame in decode_chunk_stream(data):
        grouped[frame.kind].append(frame.payload)
    return grouped


# Legacy helpers retained for compatibility with simple length-prefixed streams.
def encode_framed(payload: bytes, chunk_size: int = 8192) -> bytes:
    """
    Encode a payload as a sequence of length-prefixed DATA chunks followed by
    a 0-byte terminal signal.
    """
    chunks = [payload[i : i + chunk_size] for i in range(0, len(payload), chunk_size)]
    return encode_chunk_stream(ack_payload=b"", data_chunks=chunks)


def decode_framed(data: bytes) -> bytes:
    """
    Decode a DATA-only chunk stream, concatenating payloads and ignoring the
    leading ACK frame if present.
    """
    grouped = decode_chunk_stream_payloads(data)
    return b"".join(grouped[ChunkType.DATA])


@dataclass
class AckChunkFrame:
    """Explicit ACK-chunking frame used by wired chat channels."""

    seq: int
    payload: bytes
    final: bool = False

    def encode(self) -> bytes:
        header = f"ACK:{self.seq}:".encode("utf-8")
        body = header + self.payload
        if self.final:
            body += TERMINAL
        return encode_framed(body)

    @staticmethod
    def decode(data: bytes) -> "AckChunkFrame":
        payload = decode_framed(data)
        prefix, sep, rest = payload.partition(b":")
        if prefix != b"ACK":
            raise ValueError("Not an ACK frame")
        seq_str, sep2, body = rest.partition(b":")
        if not sep2:
            raise ValueError("Malformed ACK frame")
        return AckChunkFrame(seq=int(seq_str), payload=body, final=payload.endswith(TERMINAL))


# ---------------------------------------------------------------------------
# NATS connection helper
# ---------------------------------------------------------------------------
async def connect_nats(url: str, token: str | None = None, creds: str | None = None) -> NatsClient:
    """Connect to NATS with bounded reconnect backoff."""
    opts: dict[str, Any] = {
        "servers": [url],
        "name": "wrpc-transport-dev",
        "reconnect_time_wait": 1,
        "max_reconnect_attempts": 60,
        "ping_interval": 20,
        "max_outstanding_pings": 3,
        "error_cb": lambda e: logger.error("NATS error: %s", e),
        "disconnected_cb": lambda: logger.warning("NATS disconnected"),
        "reconnected_cb": lambda: logger.info("NATS reconnected"),
    }
    if token:
        opts["token"] = token
    if creds:
        opts["user_credentials"] = creds

    for attempt in range(1, 11):
        try:
            nc = await nats.connect(**opts)
            logger.info("Connected to NATS: %s", nc.connected_url.netloc)
            return nc
        except Exception as exc:  # noqa: BLE001
            wait = min(2**attempt, 30)
            logger.warning("NATS connect attempt %d failed: %s; retrying in %ss", attempt, exc, wait)
            await asyncio.sleep(wait)
    raise RuntimeError("Unable to connect to NATS after retries")


# ---------------------------------------------------------------------------
# wRPC caller
# ---------------------------------------------------------------------------
@dataclass
class WrpcResponse:
    """Parsed ACK-first response with separated data and query chunks."""

    ack: bytes
    data: list[bytes] = field(default_factory=list)
    queries: list[bytes] = field(default_factory=list)

    @property
    def body(self) -> bytes:
        """Concatenated DATA payloads."""
        return b"".join(self.data)

    def json(self) -> Any:
        """Parse concatenated DATA payloads as JSON."""
        return json.loads(self.body.decode("utf-8"))


class WrpcClient:
    """Minimal wRPC-over-NATS caller using request/reply framing."""

    def __init__(self, nc: NatsClient, timeout: float = 10.0):
        self.nc = nc
        self.timeout = timeout

    async def call(
        self,
        subject: str,
        payload: bytes,
        ack_payload: bytes = b"",
        chunk_size: int = 8192,
    ) -> WrpcResponse:
        """
        Send a framed request and wait for an ACK-first framed reply.

        The request body is sent as DATA chunks; the reply is parsed into
        ACK/DATA/QUERY frames and returned as a WrpcResponse.
        """
        data_chunks = [payload[i : i + chunk_size] for i in range(0, len(payload), chunk_size)]
        data = encode_chunk_stream(ack_payload=ack_payload, data_chunks=data_chunks)
        msg = await self.nc.request(subject, data, timeout=self.timeout)
        frames = decode_chunk_stream_payloads(msg.data)
        return WrpcResponse(
            ack=b"".join(frames[ChunkType.ACK]),
            data=frames[ChunkType.DATA],
            queries=frames[ChunkType.QUERY],
        )

    async def call_json(
        self,
        subject: str,
        payload: dict[str, Any],
        ack_payload: bytes = b"",
        chunk_size: int = 8192,
    ) -> Any:
        data = json.dumps(payload).encode("utf-8")
        resp = await self.call(subject, data, ack_payload=ack_payload, chunk_size=chunk_size)
        return resp.json()

    async def call_with_query(
        self,
        subject: str,
        payload: dict[str, Any],
        query_handler: Callable[[str], str] | None = None,
        ack_payload: bytes = b"",
        chunk_size: int = 8192,
    ) -> Any:
        """
        Send a request and handle any interactive QUERY chunks in the reply.

        When the server asks a question via a QUERY frame, query_handler is
        called with the question text and the answer is published back on the
        reply subject.
        """
        data = json.dumps(payload).encode("utf-8")
        data_chunks = [data[i : i + chunk_size] for i in range(0, len(data), chunk_size)]
        request_data = encode_chunk_stream(ack_payload=ack_payload, data_chunks=data_chunks)
        msg = await self.nc.request(subject, request_data, timeout=self.timeout)
        frames = decode_chunk_stream(msg.data)

        # The last DATA chunk's reply subject is where interactive answers go.
        reply_to = msg.reply
        collected: list[bytes] = []
        for frame in frames:
            if frame.kind == ChunkType.DATA:
                collected.append(frame.payload)
            elif frame.kind == ChunkType.QUERY:
                question = frame.payload.decode("utf-8")
                if query_handler is None:
                    raise RuntimeError(f"Received query but no handler: {question}")
                answer = query_handler(question).encode("utf-8")
                answer_stream = encode_chunk_stream(ack_payload=b"", data_chunks=[answer])
                await self.nc.publish(reply_to, answer_stream)

        return json.loads(b"".join(collected).decode("utf-8"))


# ---------------------------------------------------------------------------
# Subscription helpers
# ---------------------------------------------------------------------------
async def subscribe_wired_chat(
    nc: NatsClient,
    subject: str,
    handler: Callable[[AckChunkFrame], None],
) -> Subscription:
    """Subscribe to a wired-chat subject and deliver decoded ACK frames."""

    async def _cb(msg: Msg) -> None:
        try:
            frame = AckChunkFrame.decode(msg.data)
            handler(frame)
            await msg.ack()
        except Exception as exc:  # noqa: BLE001
            logger.exception("Failed to decode wired-chat frame: %s", exc)
            await msg.nak()

    sub = await nc.subscribe(subject, cb=_cb)
    return sub


async def subscribe_chunk_stream(
    nc: NatsClient,
    subject: str,
    handler: Callable[[WrpcResponse], bytes | None],
) -> Subscription:
    """
    Subscribe to a subject and deliver fully decoded ACK-first responses.

    If the handler returns non-None bytes, they are sent back as an ACK-first
    reply on msg.reply.
    """

    async def _cb(msg: Msg) -> None:
        try:
            frames = decode_chunk_stream_payloads(msg.data)
            resp = WrpcResponse(
                ack=b"".join(frames[ChunkType.ACK]),
                data=frames[ChunkType.DATA],
                queries=frames[ChunkType.QUERY],
            )
            reply = handler(resp)
            if reply is not None and msg.reply:
                ack_first = encode_chunk_stream(ack_payload=b"ACK", data_chunks=[reply])
                await msg.respond(ack_first)
        except Exception as exc:  # noqa: BLE001
            logger.exception("Chunk stream handler error: %s", exc)

    sub = await nc.subscribe(subject, cb=_cb)
    return sub


# ---------------------------------------------------------------------------
# Example service handler
# ---------------------------------------------------------------------------
async def example_prompt_handler(msg: Msg) -> None:
    """Echo-style prompt handler for local development."""
    try:
        frames = decode_chunk_stream_payloads(msg.data)
        payload = b"".join(frames[ChunkType.DATA])
        req = json.loads(payload.decode("utf-8"))
        logger.info("Received prompt: %s", req)

        response = {
            "prompt_id": req.get("id", "unknown"),
            "accepted": True,
            "payload": json.dumps({"echo": req.get("payload")}),
            "status": "ok",
        }
        if msg.reply:
            ack_first = encode_chunk_stream(
                ack_payload=b"ACK",
                data_chunks=[json.dumps(response).encode("utf-8")],
            )
            await msg.respond(ack_first)
    except Exception as exc:  # noqa: BLE001
        logger.exception("Prompt handler error: %s", exc)
        if msg.reply:
            error_stream = encode_chunk_stream(
                ack_payload=b"NACK",
                data_chunks=[json.dumps({"accepted": False, "error": str(exc)}).encode("utf-8")],
            )
            await msg.respond(error_stream)


# ---------------------------------------------------------------------------
# CLI
# ---------------------------------------------------------------------------
def build_parser() -> argparse.ArgumentParser:
    parser = argparse.ArgumentParser(description="wRPC-over-NATS development transport")
    parser.add_argument("--nats", default="nats://127.0.0.1:4222", help="NATS server URL")
    parser.add_argument("--token", default=None, help="NATS auth token")
    parser.add_argument("--creds", default=None, help="NATS credentials file")
    parser.add_argument("--timeout", type=float, default=10.0, help="Request timeout")

    sub = parser.add_subparsers(dest="command", required=True)

    prompt_cmd = sub.add_parser("prompt", help="Send an agent prompt")
    prompt_cmd.add_argument("--subject", required=True, help="Target subject")
    prompt_cmd.add_argument("--id", default="dev-001", help="Prompt ID")
    prompt_cmd.add_argument("--agent-type", default="coder", help="Agent type")
    prompt_cmd.add_argument("--owner", default="local", help="Owner")
    prompt_cmd.add_argument("--session", default="dev-session", help="Session ID")
    prompt_cmd.add_argument("--payload", default='{"task":"noop"}', help="JSON payload")

    compile_cmd = sub.add_parser("compile", help="Send a Nulang compile request")
    compile_cmd.add_argument("--subject", default="agents.nulang.compile", help="Target subject")
    compile_cmd.add_argument("--source", default="(lambda (x) (+ x 1))", help="Nulang source")

    serve_cmd = sub.add_parser("serve", help="Run a mock prompt echo server")
    serve_cmd.add_argument("--subject", required=True, help="Subject to serve")

    return parser


async def main() -> int:
    parser = build_parser()
    args = parser.parse_args()

    nc = await connect_nats(args.nats, token=args.token, creds=args.creds)
    client = WrpcClient(nc, timeout=args.timeout)

    try:
        if args.command == "prompt":
            payload = {
                "id": args.id,
                "agent_type": args.agent_type,
                "owner": args.owner,
                "session_id": args.session,
                "payload": args.payload.encode("utf-8"),
                "timeout_ms": 5000,
            }
            result = await client.call_json(args.subject, payload, ack_payload=b"ACK")
            print(json.dumps(result, indent=2))

        elif args.command == "compile":
            # Minimal AST serialization: a literal integer 42.
            ast = {"literal": {"integer": 42}}
            result = await client.call_json(args.subject, {"source": args.source, "ast": ast})
            print(json.dumps(result, indent=2))

        elif args.command == "serve":
            sub = await nc.subscribe(args.subject, cb=example_prompt_handler)
            logger.info("Serving prompt echoes on %s", args.subject)
            while True:
                await asyncio.sleep(1)
            await sub.unsubscribe()

    finally:
        await nc.drain()

    return 0


if __name__ == "__main__":
    sys.exit(asyncio.run(main()))
