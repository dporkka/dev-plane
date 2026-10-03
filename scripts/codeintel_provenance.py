#!/usr/bin/env python3
"""Attach immutable source/tool provenance to a code-intelligence bake-off suite."""

from __future__ import annotations

import argparse
import json
import os
import pathlib
import shlex
import subprocess
from typing import Any


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


def enrich_suite(suite: dict[str, Any]) -> dict[str, Any]:
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
    suite["provenance_version"] = 1
    return suite


def write_enriched_suite(path: pathlib.Path) -> None:
    suite = json.loads(path.read_text(encoding="utf-8"))
    if not isinstance(suite, dict):
        raise ValueError("suite document must be an object")
    enriched = enrich_suite(suite)
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
    args = parser.parse_args()
    if not args.suite.is_file():
        parser.error(f"suite file does not exist: {args.suite}")
    write_enriched_suite(args.suite)
    print(args.suite)
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
