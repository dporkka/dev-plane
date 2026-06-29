#!/usr/bin/env python3
"""
wrpc_transport.py -- Development helper for invoking wRPC methods over NATS.

This script provides:
  - An async NATS connection helper with bounded reconnect backoff.
  - wRPC-style framed chunk encoding/decoding.
  - Explicit ACK-chunking frames with 0-byte terminal signals for "wired chat".
  - Example invocations for agent prompt handling and Nulang compilation.

Install dependencies:
    pip install nats-py

Example:
    python wrpc_transport.py \
        --nats nats://100.64.0.10:4222 \
        --subject agents.prompt.coder.team-alpha.session-001 \
        --prompt '{"task":"hello"}'
"""

from __future__ import annotations

import argparse
import asyncio
import json
import logging
import struct
import sys
from dataclasses import dataclass
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


def encode_framed(payload: bytes, chunk_size: int = 8192) -> bytes:
    """
    Encode a payload as a sequence of length-prefixed chunks followed by a
    0-byte terminal signal.

    Each non-terminal chunk is sent as: [4-byte length][payload bytes]
    The terminal signal is a bare 0x00 byte.
    """
    out = bytearray()
    offset = 0
    while offset < len(payload):
        end = min(offset + chunk_size, len(payload))
        chunk = payload[offset:end]
        out.extend(struct.pack(CHUNK_HEADER_FMT, len(chunk)))
        out.extend(chunk)
        offset = end
    out.extend(TERMINAL)
    return bytes(out)


def decode_framed(data: bytes) -> bytes:
    """
    Decode a stream of length-prefixed chunks until a 0-byte terminal is seen.

    Raises ValueError on malformed input.
    """
    out = bytearray()
    i = 0
    while i < len(data):
        if data[i : i + 1] == TERMINAL:
            return bytes(out)
        if i + 4 > len(data):
            raise ValueError("Truncated chunk header")
        (length,) = struct.unpack(CHUNK_HEADER_FMT, data[i : i + 4])
        i += 4
        if i + length > len(data):
            raise ValueError("Truncated chunk body")
        out.extend(data[i : i + length])
        i += length
    raise ValueError("Missing terminal signal")


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
            wait = min(2 ** attempt, 30)
            logger.warning("NATS connect attempt %d failed: %s; retrying in %ss", attempt, exc, wait)
            await asyncio.sleep(wait)
    raise RuntimeError("Unable to connect to NATS after retries")


# ---------------------------------------------------------------------------
# wRPC caller
# ---------------------------------------------------------------------------
class WrpcClient:
    """Minimal wRPC-over-NATS caller using request/reply framing."""

    def __init__(self, nc: NatsClient, timeout: float = 10.0):
        self.nc = nc
        self.timeout = timeout

    async def call(
        self,
        subject: str,
        payload: bytes,
        reply_subject: str | None = None,
        chunk_size: int = 8192,
    ) -> bytes:
        """
        Send a framed request and wait for a framed reply.

        If reply_subject is None, a NATS-generated inbox is used.
        """
        data = encode_framed(payload, chunk_size=chunk_size)
        if reply_subject:
            msg = await self.nc.request(subject, data, timeout=self.timeout)
        else:
            msg = await self.nc.request(subject, data, timeout=self.timeout)
        return decode_framed(msg.data)

    async def call_json(
        self,
        subject: str,
        payload: dict[str, Any],
        reply_subject: str | None = None,
        chunk_size: int = 8192,
    ) -> dict[str, Any]:
        data = json.dumps(payload).encode("utf-8")
        raw = await self.call(subject, data, reply_subject=reply_subject, chunk_size=chunk_size)
        return json.loads(raw.decode("utf-8"))


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


# ---------------------------------------------------------------------------
# Example service handler
# ---------------------------------------------------------------------------
async def example_prompt_handler(msg: Msg) -> None:
    """Echo-style prompt handler for local development."""
    try:
        payload = decode_framed(msg.data)
        req = json.loads(payload.decode("utf-8"))
        logger.info("Received prompt: %s", req)

        response = {
            "prompt_id": req.get("id", "unknown"),
            "accepted": True,
            "payload": json.dumps({"echo": req.get("payload")}),
            "status": "ok",
        }
        if msg.reply:
            await msg.respond(encode_framed(json.dumps(response).encode("utf-8")))
    except Exception as exc:  # noqa: BLE001
        logger.exception("Prompt handler error: %s", exc)
        if msg.reply:
            await msg.respond(
                encode_framed(json.dumps({"accepted": False, "error": str(exc)}).encode("utf-8"))
            )


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
            result = await client.call_json(args.subject, payload)
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
