#!/usr/bin/env python3
import importlib.util
import json
import pathlib
import subprocess
import sys
import tempfile
import unittest
from unittest import mock

MODULE_PATH = pathlib.Path(__file__).with_name("codeintel_provenance.py")
spec = importlib.util.spec_from_file_location("codeintel_provenance", MODULE_PATH)
provenance = importlib.util.module_from_spec(spec)
sys.modules[spec.name] = provenance
spec.loader.exec_module(provenance)


class CodeIntelProvenanceTests(unittest.TestCase):
    def test_repo_revision_returns_exact_head_sha(self):
        with tempfile.TemporaryDirectory() as tmp:
            repo = pathlib.Path(tmp) / "repo"
            repo.mkdir()
            subprocess.run(["git", "init", "-q", str(repo)], check=True)
            subprocess.run(["git", "-C", str(repo), "config", "user.email", "test@example.com"], check=True)
            subprocess.run(["git", "-C", str(repo), "config", "user.name", "Test"], check=True)
            (repo / "a.txt").write_text("hello\n", encoding="utf-8")
            subprocess.run(["git", "-C", str(repo), "add", "a.txt"], check=True)
            subprocess.run(["git", "-C", str(repo), "commit", "-qm", "initial"], check=True)
            expected = subprocess.run(
                ["git", "-C", str(repo), "rev-parse", "HEAD"],
                check=True,
                text=True,
                stdout=subprocess.PIPE,
            ).stdout.strip()

            self.assertEqual(provenance.repo_revision(repo), expected)

    def test_command_version_returns_first_nonempty_output_line(self):
        completed = subprocess.CompletedProcess(
            args=["tool", "--version"],
            returncode=0,
            stdout="\ncodebase-memory-mcp 0.11.0\nextra\n",
            stderr="",
        )
        with mock.patch.object(provenance.subprocess, "run", return_value=completed):
            self.assertEqual(
                provenance.command_version(["tool", "--version"]),
                "codebase-memory-mcp 0.11.0",
            )

    def test_command_version_records_failure_without_raising(self):
        completed = subprocess.CompletedProcess(
            args=["tool", "--version"],
            returncode=2,
            stdout="",
            stderr="boom\n",
        )
        with mock.patch.object(provenance.subprocess, "run", return_value=completed):
            self.assertEqual(provenance.command_version(["tool", "--version"]), "error: boom")

    def test_enrich_suite_records_versions_and_repository_revisions(self):
        suite = {
            "version": 1,
            "scenarios": [
                {
                    "id": "s1",
                    "repository": "owner/repo",
                    "repo_path": "/repos/repo",
                    "runs": [],
                },
                {
                    "id": "s2",
                    "repository": "owner/repo",
                    "repo_path": "/repos/repo",
                    "runs": [],
                },
            ],
        }
        with mock.patch.object(provenance, "repo_revision", return_value="abc123"), mock.patch.object(
            provenance,
            "collect_tool_versions",
            return_value={"codebase-memory": "0.11.0", "gitnexus": "1.6.12"},
        ):
            enriched = provenance.enrich_suite(suite)

        self.assertEqual(enriched["repositories"], {"owner/repo": {"path": "/repos/repo", "revision": "abc123"}})
        self.assertEqual(enriched["tools"]["gitnexus"], "1.6.12")
        self.assertEqual(enriched["scenarios"][0]["revision"], "abc123")
        self.assertEqual(enriched["scenarios"][1]["revision"], "abc123")

    def test_write_enriched_suite_replaces_file_atomically(self):
        with tempfile.TemporaryDirectory() as tmp:
            path = pathlib.Path(tmp) / "suite.json"
            path.write_text(json.dumps({"version": 1, "scenarios": []}), encoding="utf-8")
            with mock.patch.object(provenance, "enrich_suite", return_value={"version": 2, "scenarios": []}):
                provenance.write_enriched_suite(path)
            self.assertEqual(json.loads(path.read_text(encoding="utf-8"))["version"], 2)


if __name__ == "__main__":
    unittest.main()
