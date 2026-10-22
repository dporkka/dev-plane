#!/usr/bin/env python3
"""Attach immutable source, tool, and corpus provenance to a bake-off suite."""

from __future__ import annotations

import argparse
import hashlib
import json
import os
import pathlib
import shlex
import subprocess
from typing import Any

DEFAULT_CORPUS = pathlib.Path("benchmarks/code-intelligence/scenarios.json")


def repo_revision(repo_path: pathlib.Path) -> str:
    completed = subprocess.run(
        ["git", "-C", str(repo_path), "rev-parse", "HEAD"],
        check=True,
        capture_output=True,
        text=True,
        encoding="utf-8",
        errors="replace",
    )
    return completed.stdout.strip()


def file_sha256(path: pathlib.Path) -> str:
    digest = hashlib.sha256()
    with path.open("rb") as handle:
        for chunk in iter(lambda: handle.read(1024 * 1024), b""):
            digest.update(chunk)
    return digest.hexdigest()


def command_version(command: list[str]) -> str:
    try:
        completed = subprocess.run(
            command,
            check=False,
            capture_output=True,
            text=True,
            encoding="utf-8",
            errors="replace",
            timeout=30,
        )
    except Exception as exc:
        return f"error: {exc}"

    combined = "\n".join(part for part in (completed.stdout, completed.stderr) if part)
    lines = [line.strip() for line in combined.splitlines() if line.strip()]
    if completed.returncode != 0:
        detail = lines[0] if lines else f"exit {completed.returncode}"
        return f"error: {detail}"
    return lines[0] if lines else "unknown"


def env_command(name: str, default: str) -> list[str]:
    value = os.environ.get(name, default)
    command = shlex.split(value)
    if not command:
        raise ValueError(f"{name} resolved to an empty command")
    return command


def collect_tool_versions() -> dict[str, str]:
    return {
        "codebase-memory": command_version(
            env_command(
                "CODEINTEL_CODEBASE_MEMORY_VERSION_CMD",
                "codebase-memory-mcp --version",
            )
        ),
        "gitnexus": command_version(
            env_command(
                "CODEINTEL_GITNEXUS_VERSION_CMD",
                "npx -y gitnexus@1.6.12 --version",
            )
        ),
        "python": command_version(["python3", "--version"]),
        "go": command_version(["go", "version"]),
        "node": command_version(["node", "--version"]),
    }


def safe_repo_revision(repo_path: pathlib.Path) -> str:
    try:
        return repo_revision(repo_path)
    except Exception as exc:
        return f"error: {exc}"


def corpus_scenarios_by_id(corpus_document: dict[str, Any]) -> dict[str, dict[str, Any]]:
    scenarios = corpus_document.get("scenarios")
    if not isinstance(scenarios, list):
        raise ValueError("corpus.scenarios must be an array")

    by_id: dict[str, dict[str, Any]] = {}
    for scenario in scenarios:
        if not isinstance(scenario, dict):
            raise ValueError(f"invalid corpus scenario entry: {scenario!r}")
        scenario_id = scenario.get("id")
        if not isinstance(scenario_id, str) or not scenario_id:
            raise ValueError(f"corpus scenario id is required: {scenario!r}")
        if scenario_id in by_id:
            raise ValueError(f"duplicate corpus scenario id: {scenario_id}")
        by_id[scenario_id] = scenario
    return by_id


def attach_corpus_provenance(
    suite: dict[str, Any],
    corpus_document: dict[str, Any],
    corpus_sha256: str,
) -> None:
    corpus_by_id = corpus_scenarios_by_id(corpus_document)
    scenarios = suite.get("scenarios")
    if not isinstance(scenarios, list):
        raise ValueError("suite.scenarios must be an array")

    for scenario in scenarios:
        if not isinstance(scenario, dict):
            raise ValueError(f"invalid suite scenario entry: {scenario!r}")
        scenario_id = scenario.get("id")
        if not isinstance(scenario_id, str) or not scenario_id:
            raise ValueError(f"suite scenario id is required: {scenario!r}")
        source = corpus_by_id.get(scenario_id)
        if source is None:
            raise ValueError(f"suite scenario {scenario_id!r} is missing from corpus")
        if scenario.get("expected") != source.get("expected"):
            raise ValueError(f"suite scenario {scenario_id!r} expected results differ from corpus")

        scenario["intent"] = source.get("intent")
        scenario["prompt"] = source.get("prompt")
        scenario["backend_calls"] = source.get("backends")

    suite["corpus"] = {
        "version": corpus_document.get("version"),
        "sha256": corpus_sha256,
    }


def enrich_suite(
    suite: dict[str, Any],
    *,
    corpus_document: dict[str, Any] | None = None,
    corpus_sha256: str | None = None,
) -> dict[str, Any]:
    scenarios = suite.get("scenarios")
    if not isinstance(scenarios, list):
        raise ValueError("suite.scenarios must be an array")

    repositories: dict[str, dict[str, str]] = {}
    for scenario in scenarios:
        if not isinstance(scenario, dict):
            raise ValueError(f"invalid scenario entry: {scenario!r}")
        repository = scenario.get("repository")
        raw_path = scenario.get("repo_path")
        if not isinstance(repository, str) or not repository:
            continue
        if not isinstance(raw_path, str) or not raw_path:
            continue

        entry = repositories.get(repository)
        if entry is None:
            repo_path = pathlib.Path(raw_path)
            entry = {
                "path": raw_path,
                "revision": safe_repo_revision(repo_path),
            }
            repositories[repository] = entry
        scenario["revision"] = entry["revision"]

    suite["repositories"] = repositories
    suite["tools"] = collect_tool_versions()

    if corpus_document is not None:
        if not isinstance(corpus_sha256, str) or not corpus_sha256:
            raise ValueError("corpus_sha256 is required when corpus_document is provided")
        attach_corpus_provenance(suite, corpus_document, corpus_sha256)

    suite["provenance_version"] = 2
    return suite


def write_enriched_suite(
    path: pathlib.Path,
    *,
    corpus_path: pathlib.Path = DEFAULT_CORPUS,
) -> None:
    suite = json.loads(path.read_text(encoding="utf-8"))
    if not isinstance(suite, dict):
        raise ValueError("suite document must be an object")

    corpus_document = json.loads(corpus_path.read_text(encoding="utf-8"))
    if not isinstance(corpus_document, dict):
        raise ValueError("corpus document must be an object")

    enriched = enrich_suite(
        suite,
        corpus_document=corpus_document,
        corpus_sha256=file_sha256(corpus_path),
    )
    enriched["corpus"]["path"] = str(corpus_path)

    tmp = path.with_name(path.name + ".tmp")
    tmp.write_text(json.dumps(enriched, indent=2) + "\n", encoding="utf-8")
    tmp.replace(path)


def main() -> int:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument(
        "suite",
        nargs="?",
        type=pathlib.Path,
        default=pathlib.Path("data/codeintel-bakeoff/suite.json"),
    )
    parser.add_argument(
        "--corpus",
        type=pathlib.Path,
        default=DEFAULT_CORPUS,
        help="benchmark corpus whose exact bytes and query definitions should be recorded",
    )
    args = parser.parse_args()
    if not args.suite.is_file():
        parser.error(f"suite file does not exist: {args.suite}")
    if not args.corpus.is_file():
        parser.error(f"corpus file does not exist: {args.corpus}")
    write_enriched_suite(args.suite, corpus_path=args.corpus)
    print(args.suite)
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
