#!/usr/bin/env python3
import importlib.util
import pathlib
import sys
import tempfile
import unittest

MODULE_PATH = pathlib.Path(__file__).with_name("codeintel_bakeoff.py")
spec = importlib.util.spec_from_file_location("codeintel_bakeoff", MODULE_PATH)
codeintel = importlib.util.module_from_spec(spec)
sys.modules[spec.name] = codeintel
spec.loader.exec_module(codeintel)


class CodeIntelBakeoffTests(unittest.TestCase):
    def test_extract_known_paths_returns_only_paths_present_in_backend_output(self):
        known = [
            "apps/api/services/proposal-service.ts",
            "apps/api/proposals/actions.ts",
            "README.md",
        ]
        raw = (
            "Relevant: apps/api/services/proposal-service.ts\n"
            "Caller: /tmp/adacavo/apps/api/proposals/actions.ts:42\n"
            "Noise: https://example.com/README.md\n"
        )

        self.assertEqual(
            codeintel.extract_known_paths(raw, known),
            [
                "apps/api/services/proposal-service.ts",
                "apps/api/proposals/actions.ts",
                "README.md",
            ],
        )

    def test_resolve_repo_path_uses_repository_basename(self):
        with tempfile.TemporaryDirectory() as tmp:
            root = pathlib.Path(tmp)
            repo = root / "nulang-cloud"
            repo.mkdir()
            self.assertEqual(
                codeintel.resolve_repo_path(root, "dporkka/nulang-cloud"),
                repo,
            )

    def test_validate_scenario_requires_backend_calls(self):
        scenario = {
            "id": "s1",
            "repository": "owner/repo",
            "expected": ["a.go"],
            "backends": {
                "codebase-memory": [
                    {"tool": "search_graph", "arguments": {"semantic_query": ["x"]}}
                ],
                "gitnexus": [
                    {"tool": "query", "arguments": {"search_query": "x"}}
                ],
            },
        }
        codeintel.validate_scenario(scenario)

        broken = dict(scenario)
        broken["backends"] = {"codebase-memory": []}
        with self.assertRaises(ValueError):
            codeintel.validate_scenario(broken)

    def test_content_text_includes_structured_mcp_payload(self):
        payload = {
            "content": [{"type": "text", "text": "src/main.rs"}],
            "structuredContent": {"files": ["src/repl.rs"]},
        }
        text = codeintel.mcp_result_text(payload)
        self.assertIn("src/main.rs", text)
        self.assertIn("src/repl.rs", text)


if __name__ == "__main__":
    unittest.main()
