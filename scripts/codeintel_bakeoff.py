#!/usr/bin/env python3
"""Run reproducible GitNexus vs codebase-memory retrieval benchmarks.

The harness intentionally keeps indexing separate from query latency and scores only
repository file paths actually mentioned by each backend response. It uses MCP for
query execution so the benchmark exercises the same tool surface agents use.
"""

from __future__ import annotations

import argparse
import json
import os
import pathlib
import queue
import shlex
import shutil
import subprocess
import sys
import threading
import time
from dataclasses import dataclass
from datetime import datetime, timezone
from typing import Any

BACKEND_CODEBASE_MEMORY = "codebase-memory"
BACKEND_GITNEXUS = "gitnexus"
SUPPORTED_BACKENDS = (BACKEND_CODEBASE_MEMORY, BACKEND_GITNEXUS)


@dataclass
class Observation:
    backend: str
    results: list[str]
    latency_ms: int
    output_tokens: int
    error: str = ""
    raw_output: str = ""
    index_ms: int = 0

    def score_input(self) -> dict[str, Any]:
        return {
            "backend": self.backend,
            "results": self.results,
            "latency_ms": self.latency_ms,
            "output_tokens": self.output_tokens,
            "error": self.error,
        }


class MCPClient:
    def __init__(self, command: list[str], cwd: pathlib.Path, timeout_s: float = 60.0):
        self.command = command
        self.cwd = cwd
        self.timeout_s = timeout_s
        self._responses: queue.Queue[dict[str, Any]] = queue.Queue()
        self._stderr: list[str] = []
        self._next_id = 1
        self._process = subprocess.Popen(
            command,
            cwd=str(cwd),
            stdin=subprocess.PIPE,
            stdout=subprocess.PIPE,
            stderr=subprocess.PIPE,
            text=True,
            encoding="utf-8",
            errors="replace",
            bufsize=1,
        )
        assert self._process.stdout is not None
        assert self._process.stderr is not None
        self._stdout_thread = threading.Thread(target=self._read_stdout, daemon=True)
        self._stderr_thread = threading.Thread(target=self._read_stderr, daemon=True)
        self._stdout_thread.start()
        self._stderr_thread.start()
        self._initialize()

    def _read_stdout(self) -> None:
        assert self._process.stdout is not None
        for line in self._process.stdout:
            line = line.strip()
            if not line:
                continue
            try:
                payload = json.loads(line)
            except json.JSONDecodeError:
                continue
            if isinstance(payload, dict):
                self._responses.put(payload)

    def _read_stderr(self) -> None:
        assert self._process.stderr is not None
        for line in self._process.stderr:
            self._stderr.append(line.rstrip())

    def _send(self, payload: dict[str, Any]) -> None:
        if self._process.poll() is not None:
            raise RuntimeError(self._failure_message("MCP server exited before request"))
        assert self._process.stdin is not None
        self._process.stdin.write(json.dumps(payload, separators=(",", ":")) + "\n")
        self._process.stdin.flush()

    def _request(self, method: str, params: dict[str, Any]) -> dict[str, Any]:
        request_id = self._next_id
        self._next_id += 1
        self._send(
            {
                "jsonrpc": "2.0",
                "id": request_id,
                "method": method,
                "params": params,
            }
        )
        deadline = time.monotonic() + self.timeout_s
        while True:
            remaining = deadline - time.monotonic()
            if remaining <= 0:
                raise TimeoutError(self._failure_message(f"timeout waiting for {method}"))
            try:
                payload = self._responses.get(timeout=min(remaining, 0.25))
            except queue.Empty:
                if self._process.poll() is not None:
                    raise RuntimeError(self._failure_message(f"MCP server exited during {method}"))
                continue
            if payload.get("id") != request_id:
                continue
            if "error" in payload:
                raise RuntimeError(f"{method}: {payload['error']}")
            result = payload.get("result")
            return result if isinstance(result, dict) else {"value": result}

    def _initialize(self) -> None:
        self._request(
            "initialize",
            {
                "protocolVersion": "2024-11-05",
                "capabilities": {},
                "clientInfo": {"name": "dev-plane-codeintel-bakeoff", "version": "1"},
            },
        )
        self._send({"jsonrpc": "2.0", "method": "notifications/initialized", "params": {}})

    def call_tool(self, name: str, arguments: dict[str, Any]) -> dict[str, Any]:
        return self._request("tools/call", {"name": name, "arguments": arguments})

    def close(self) -> None:
        if self._process.poll() is None:
            self._process.terminate()
            try:
                self._process.wait(timeout=3)
            except subprocess.TimeoutExpired:
                self._process.kill()
                self._process.wait(timeout=3)

    def _failure_message(self, message: str) -> str:
        stderr = "\n".join(self._stderr[-20:]).strip()
        return f"{message}: {stderr}" if stderr else message

    def __enter__(self) -> "MCPClient":
        return self

    def __exit__(self, exc_type, exc, tb) -> None:
        self.close()


def extract_known_paths(text: str, known_paths: list[str]) -> list[str]:
    return sorted({path for path in known_paths if path and path in text})


def resolve_repo_path(root: pathlib.Path, repository: str) -> pathlib.Path:
    basename = repository.rsplit("/", 1)[-1]
    candidates = (root / basename, root / repository)
    for candidate in candidates:
        if candidate.is_dir():
            return candidate
    raise FileNotFoundError(
        f"repository {repository!r} not found under {root}; tried "
        + ", ".join(str(candidate) for candidate in candidates)
    )


def validate_scenario(scenario: dict[str, Any]) -> None:
    for field in ("id", "repository", "expected", "backends"):
        if not scenario.get(field):
            raise ValueError(f"scenario missing required field {field!r}: {scenario!r}")
    backends = scenario["backends"]
    if not isinstance(backends, dict):
        raise ValueError(f"scenario backends must be an object: {scenario['id']}")
    for backend in SUPPORTED_BACKENDS:
        calls = backends.get(backend)
        if not isinstance(calls, list) or not calls:
            raise ValueError(f"scenario {scenario['id']} requires calls for {backend}")
        for call in calls:
            if not isinstance(call, dict) or not call.get("tool"):
                raise ValueError(f"scenario {scenario['id']} has invalid {backend} call: {call!r}")
            if "arguments" in call and not isinstance(call["arguments"], dict):
                raise ValueError(f"scenario {scenario['id']} call arguments must be an object")


def mcp_result_text(payload: dict[str, Any]) -> str:
    pieces: list[str] = []
    content = payload.get("content")
    if isinstance(content, list):
        for item in content:
            if isinstance(item, dict) and isinstance(item.get("text"), str):
                pieces.append(item["text"])
    structured = payload.get("structuredContent")
    if structured is not None:
        pieces.append(json.dumps(structured, sort_keys=True))
    if not pieces:
        pieces.append(json.dumps(payload, sort_keys=True))
    return "\n".join(pieces)


def tracked_files(repo_path: pathlib.Path) -> list[str]:
    completed = subprocess.run(
        ["git", "-C", str(repo_path), "ls-files", "-z"],
        check=True,
        stdout=subprocess.PIPE,
        stderr=subprocess.PIPE,
    )
    return sorted(
        item.decode("utf-8", errors="replace")
        for item in completed.stdout.split(b"\0")
        if item
    )


def shell_command(env_name: str, default: str) -> list[str]:
    value = os.environ.get(env_name, default)
    command = shlex.split(value)
    if not command:
        raise ValueError(f"{env_name} resolved to an empty command")
    return command


def command_available(command: list[str]) -> bool:
    executable = command[0]
    if os.path.sep in executable:
        return pathlib.Path(executable).exists()
    return shutil.which(executable) is not None


def run_index(backend: str, repo_path: pathlib.Path, timeout_s: float) -> tuple[int, str]:
    if backend == BACKEND_CODEBASE_MEMORY:
        command = shell_command("CODEINTEL_CODEBASE_MEMORY_CMD", "codebase-memory-mcp")
        command += [
            "cli",
            "index_repository",
            json.dumps({"repo_path": str(repo_path.resolve())}, separators=(",", ":")),
        ]
    elif backend == BACKEND_GITNEXUS:
        command = shell_command(
            "CODEINTEL_GITNEXUS_ANALYZE_CMD",
            "npx -y gitnexus@1.6.12 analyze --index-only",
        )
    else:
        raise ValueError(f"unsupported backend {backend}")

    if not command_available(command):
        return 0, f"index command not found: {command[0]}"

    started = time.monotonic()
    try:
        completed = subprocess.run(
            command,
            cwd=str(repo_path),
            capture_output=True,
            text=True,
            encoding="utf-8",
            errors="replace",
            timeout=timeout_s,
        )
    except subprocess.TimeoutExpired:
        return int((time.monotonic() - started) * 1000), f"index timed out after {timeout_s:.0f}s"
    elapsed_ms = int((time.monotonic() - started) * 1000)
    if completed.returncode != 0:
        detail = (completed.stderr or completed.stdout).strip()
        return elapsed_ms, f"index failed ({completed.returncode}): {detail[-2000:]}"
    return elapsed_ms, ""


def query_command(backend: str) -> list[str]:
    if backend == BACKEND_CODEBASE_MEMORY:
        return shell_command("CODEINTEL_CODEBASE_MEMORY_CMD", "codebase-memory-mcp")
    if backend == BACKEND_GITNEXUS:
        return shell_command(
            "CODEINTEL_GITNEXUS_MCP_CMD",
            "npx -y gitnexus@1.6.12 mcp",
        )
    raise ValueError(f"unsupported backend {backend}")


def run_scenario_backend(
    scenario: dict[str, Any],
    backend: str,
    repo_path: pathlib.Path,
    known_paths: list[str],
    index_ms: int,
    index_error: str,
    timeout_s: float,
) -> Observation:
    if index_error:
        return Observation(
            backend=backend,
            results=[],
            latency_ms=0,
            output_tokens=0,
            error=index_error,
            index_ms=index_ms,
        )

    command = query_command(backend)
    if not command_available(command):
        return Observation(
            backend=backend,
            results=[],
            latency_ms=0,
            output_tokens=0,
            error=f"MCP command not found: {command[0]}",
            index_ms=index_ms,
        )

    calls = scenario["backends"][backend]
    raw_parts: list[str] = []
    started = time.monotonic()
    try:
        with MCPClient(command, repo_path, timeout_s=timeout_s) as client:
            for call in calls:
                payload = client.call_tool(call["tool"], call.get("arguments", {}))
                raw_parts.append(mcp_result_text(payload))
    except Exception as exc:  # benchmark failure should be recorded, not abort the suite
        latency_ms = int((time.monotonic() - started) * 1000)
        return Observation(
            backend=backend,
            results=[],
            latency_ms=latency_ms,
            output_tokens=0,
            error=str(exc),
            index_ms=index_ms,
        )

    latency_ms = int((time.monotonic() - started) * 1000)
    raw = "\n".join(raw_parts)
    paths = extract_known_paths(raw, known_paths)
    return Observation(
        backend=backend,
        results=paths,
        latency_ms=latency_ms,
        output_tokens=max(1, (len(raw) + 3) // 4),
        raw_output=raw,
        index_ms=index_ms,
    )


def parse_repo_overrides(values: list[str]) -> dict[str, pathlib.Path]:
    overrides: dict[str, pathlib.Path] = {}
    for value in values:
        if "=" not in value:
            raise ValueError(f"--repo requires owner/name=/absolute/path, got {value!r}")
        repository, raw_path = value.split("=", 1)
        path = pathlib.Path(raw_path).expanduser().resolve()
        if not path.is_dir():
            raise FileNotFoundError(f"override for {repository} is not a directory: {path}")
        overrides[repository] = path
    return overrides


def load_scenarios(path: pathlib.Path) -> list[dict[str, Any]]:
    document = json.loads(path.read_text(encoding="utf-8"))
    scenarios = document.get("scenarios")
    if not isinstance(scenarios, list) or not scenarios:
        raise ValueError(f"{path} does not contain a non-empty scenarios array")
    for scenario in scenarios:
        if not isinstance(scenario, dict):
            raise ValueError(f"invalid scenario entry: {scenario!r}")
        validate_scenario(scenario)
    return scenarios


def main(argv: list[str] | None = None) -> int:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument(
        "--scenarios",
        type=pathlib.Path,
        default=pathlib.Path("benchmarks/code-intelligence/scenarios.json"),
    )
    parser.add_argument(
        "--repos-root",
        type=pathlib.Path,
        default=pathlib.Path(os.environ.get("CODEINTEL_REPOS_ROOT", "~/src")).expanduser(),
    )
    parser.add_argument(
        "--repo",
        action="append",
        default=[],
        metavar="OWNER/NAME=/PATH",
        help="override a repository checkout path; repeat as needed",
    )
    parser.add_argument(
        "--backend",
        action="append",
        choices=SUPPORTED_BACKENDS,
        help="run only this backend; repeat to select both",
    )
    parser.add_argument("--skip-index", action="store_true")
    parser.add_argument("--timeout", type=float, default=300.0)
    parser.add_argument(
        "--output-dir",
        type=pathlib.Path,
        default=pathlib.Path("data/codeintel-bakeoff"),
    )
    args = parser.parse_args(argv)

    scenarios = load_scenarios(args.scenarios)
    backends = tuple(dict.fromkeys(args.backend or SUPPORTED_BACKENDS))
    overrides = parse_repo_overrides(args.repo)
    output_dir = args.output_dir
    output_dir.mkdir(parents=True, exist_ok=True)

    suite_output: dict[str, Any] = {
        "version": 1,
        "generated_at": datetime.now(timezone.utc).isoformat(),
        "backends": list(backends),
        "scenarios": [],
    }

    had_failure = False
    for scenario in scenarios:
        repository = scenario["repository"]
        try:
            repo_path = overrides.get(repository) or resolve_repo_path(args.repos_root, repository)
            known_paths = tracked_files(repo_path)
        except Exception as exc:
            print(f"[{scenario['id']}] repository setup failed: {exc}", file=sys.stderr)
            had_failure = True
            continue

        observations: list[Observation] = []
        for backend in backends:
            index_ms, index_error = (0, "")
            if not args.skip_index:
                index_ms, index_error = run_index(backend, repo_path, args.timeout)
            observation = run_scenario_backend(
                scenario,
                backend,
                repo_path,
                known_paths,
                index_ms,
                index_error,
                args.timeout,
            )
            observations.append(observation)
            state = "FAIL" if observation.error else "OK"
            print(
                f"[{scenario['id']}] {backend}: {state} "
                f"files={len(observation.results)} query={observation.latency_ms}ms "
                f"index={observation.index_ms}ms",
                file=sys.stderr,
            )
            had_failure = had_failure or bool(observation.error)

        score_input = {
            "scenario": scenario["id"],
            "expected": scenario["expected"],
            "runs": [observation.score_input() for observation in observations],
        }
        scenario_path = output_dir / f"{scenario['id']}.json"
        scenario_path.write_text(json.dumps(score_input, indent=2) + "\n", encoding="utf-8")

        raw_dir = output_dir / "raw" / scenario["id"]
        raw_dir.mkdir(parents=True, exist_ok=True)
        for observation in observations:
            if observation.raw_output:
                (raw_dir / f"{observation.backend}.txt").write_text(
                    observation.raw_output,
                    encoding="utf-8",
                )

        suite_output["scenarios"].append(
            {
                "id": scenario["id"],
                "repository": repository,
                "repo_path": str(repo_path),
                "expected": scenario["expected"],
                "runs": [
                    {
                        **observation.score_input(),
                        "index_ms": observation.index_ms,
                    }
                    for observation in observations
                ],
                "score_input": str(scenario_path),
            }
        )

    summary_path = output_dir / "suite.json"
    summary_path.write_text(json.dumps(suite_output, indent=2) + "\n", encoding="utf-8")
    print(f"suite results: {summary_path}", file=sys.stderr)
    return 1 if had_failure else 0


if __name__ == "__main__":
    raise SystemExit(main())
