<!-- gitnexus:start -->
# GitNexus — Code Intelligence

This project is indexed by GitNexus as **dev-plane** (5801 symbols, 16255 relationships, 300 execution flows). Prefer GitNexus MCP tools to understand code, assess impact, and navigate safely.

> Index stale? Run `node .gitnexus/run.cjs analyze` from the project root — it auto-selects an available runner. No `.gitnexus/run.cjs` yet? `npx gitnexus analyze` (npm 11 crash → `npm i -g gitnexus`; #1939).

## Always Do

- **MUST perform impact analysis before editing any symbol.** Prefer `impact({target: "symbolName", direction: "upstream"})`. If GitNexus is unavailable, use the best available fallback: language-server references/call hierarchy, repository-wide call-site search, dependency analysis, and focused tests. Report the blast radius and the analysis method used.
- **MUST verify affected scope before committing.** Prefer `detect_changes({scope: "compare", base_ref: "main"})`. If GitNexus is unavailable, inspect the branch diff, re-run call-site/dependency searches for changed symbols, and run focused tests plus the repository's normal validation commands.
- **MUST warn the user** if impact analysis identifies HIGH or CRITICAL risk before proceeding with edits.
- When GitNexus is available, use `query({search_query: "concept"})` to find execution flows instead of grepping unfamiliar code.
- When GitNexus is available and you need full context on a specific symbol, use `context({name: "symbolName"})`.
- For security review, prefer `explain({target: "fileOrSymbol"})` when available (source→sink flows; needs `analyze --pdg`).

## Never Do

- NEVER edit a function, class, or method without first performing impact analysis using GitNexus or the documented fallback.
- NEVER ignore HIGH or CRITICAL risk warnings from impact analysis.
- NEVER rename symbols with blind find-and-replace. Prefer GitNexus `rename`; otherwise use language-aware tooling and verify all references.
- NEVER commit changes without checking affected scope using GitNexus or the documented fallback.

## Resources

| Resource | Use for |
|----------|---------|
| `gitnexus://repo/dev-plane/context` | Codebase overview, check index freshness |
| `gitnexus://repo/dev-plane/clusters` | All functional areas |
| `gitnexus://repo/dev-plane/processes` | All execution flows |
| `gitnexus://repo/dev-plane/process/{name}` | Step-by-step execution trace |

## CLI

| Task | Read this skill file |
|------|---------------------|
| Understand architecture / "How does X work?" | `.claude/skills/gitnexus/gitnexus-exploring/SKILL.md` |
| Blast radius / "What breaks if I change X?" | `.claude/skills/gitnexus/gitnexus-impact-analysis/SKILL.md` |
| Trace bugs / "Why is X failing?" | `.claude/skills/gitnexus/gitnexus-debugging/SKILL.md` |
| Rename / extract / split / refactor | `.claude/skills/gitnexus/gitnexus-refactoring/SKILL.md` |
| Tools, resources, schema reference | `.claude/skills/gitnexus/gitnexus-guide/SKILL.md` |
| Index, status, clean, wiki CLI commands | `.claude/skills/gitnexus/gitnexus-cli/SKILL.md` |

<!-- gitnexus:end -->
