#!/usr/bin/env python3
import json
import pathlib
import unittest

ROOT = pathlib.Path(__file__).resolve().parents[1]
CORPUS = ROOT / "benchmarks" / "code-intelligence" / "scenarios.json"


class CodeIntelCorpusTests(unittest.TestCase):
    @classmethod
    def setUpClass(cls):
        cls.document = json.loads(CORPUS.read_text(encoding="utf-8"))
        cls.scenarios = cls.document["scenarios"]

    def test_scenario_ids_and_expected_paths_are_unique(self):
        ids = [scenario["id"] for scenario in self.scenarios]
        self.assertEqual(len(ids), len(set(ids)), "scenario IDs must be unique")

        for scenario in self.scenarios:
            expected = scenario["expected"]
            self.assertTrue(expected, f"{scenario['id']}: expected paths must not be empty")
            self.assertEqual(
                len(expected),
                len(set(expected)),
                f"{scenario['id']}: expected paths must be unique",
            )
            for path in expected:
                self.assertIsInstance(path, str)
                self.assertTrue(path.strip(), f"{scenario['id']}: empty expected path")
                self.assertFalse(path.startswith("/"), f"{scenario['id']}: expected path must be repo-relative")

    def test_each_scenario_has_exactly_the_two_benchmark_backends(self):
        for scenario in self.scenarios:
            self.assertEqual(
                set(scenario["backends"]),
                {"codebase-memory", "gitnexus"},
                f"{scenario['id']}: backend set changed",
            )
            for backend, calls in scenario["backends"].items():
                self.assertIsInstance(calls, list)
                self.assertTrue(calls, f"{scenario['id']}: {backend} requires at least one call")

    def test_codebase_memory_calls_are_project_scoped_and_match_pinned_contract(self):
        allowed_projects = {
            "dporkka/adacavo": "adacavo",
            "nulang-org/nulang": "nulang",
            "dporkka/nulang-cloud": "nulang-cloud",
            "dporkka/dev-plane": "dev-plane",
        }
        for scenario in self.scenarios:
            expected_project = allowed_projects[scenario["repository"]]
            for call in scenario["backends"]["codebase-memory"]:
                tool = call["tool"]
                args = call.get("arguments", {})
                self.assertEqual(
                    args.get("project"),
                    expected_project,
                    f"{scenario['id']}: codebase-memory call must target the indexed project explicitly",
                )

                if tool == "search_graph":
                    self.assertIsInstance(args.get("query"), str)
                    self.assertTrue(args["query"].strip())
                    self.assertIsInstance(args.get("limit"), int)
                    self.assertGreater(args["limit"], 0)
                    self.assertNotIn("semantic_query", args)
                elif tool == "trace_path":
                    self.assertIsInstance(args.get("function_name"), str)
                    self.assertTrue(args["function_name"].strip())
                    self.assertIn(args.get("direction"), {"inbound", "outbound", "both"})
                else:
                    self.fail(f"{scenario['id']}: unsupported codebase-memory benchmark tool {tool!r}")

    def test_gitnexus_calls_match_pinned_contract(self):
        for scenario in self.scenarios:
            for call in scenario["backends"]["gitnexus"]:
                tool = call["tool"]
                args = call.get("arguments", {})
                if tool == "query":
                    self.assertIsInstance(args.get("search_query"), str)
                    self.assertTrue(args["search_query"].strip())
                    self.assertIsInstance(args.get("maxTokens"), int)
                    self.assertGreater(args["maxTokens"], 0)
                elif tool == "impact":
                    self.assertIsInstance(args.get("target"), str)
                    self.assertTrue(args["target"].strip())
                    self.assertIn(args.get("direction"), {"upstream", "downstream"})
                    self.assertIsInstance(args.get("summaryOnly"), bool)
                    self.assertIsInstance(args.get("maxTokens"), int)
                    self.assertGreater(args["maxTokens"], 0)
                else:
                    self.fail(f"{scenario['id']}: unsupported GitNexus benchmark tool {tool!r}")


if __name__ == "__main__":
    unittest.main()
