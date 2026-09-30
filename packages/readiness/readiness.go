package readiness

import (
	"encoding/json"
	"fmt"
	"io/fs"
	"path"
	"sort"
	"strings"
)

type Status string

const (
	StatusReady     Status = "ready"
	StatusAttention Status = "attention"
	StatusBlocked   Status = "blocked"
)

type Check struct {
	ID             string   `json:"id"`
	Title          string   `json:"title"`
	Status         Status   `json:"status"`
	Critical       bool     `json:"critical"`
	Evidence       []string `json:"evidence,omitempty"`
	Recommendation string   `json:"recommendation,omitempty"`
}

type Report struct {
	Status Status  `json:"status"`
	Checks []Check `json:"checks"`
}

type inventory struct {
	files map[string]struct{}
}

func Inspect(repo fs.FS) (Report, error) {
	inv := inventory{files: map[string]struct{}{}}
	if err := fs.WalkDir(repo, ".", func(name string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() {
			base := path.Base(name)
			if base == ".git" || base == "node_modules" || base == "vendor" || base == ".next" || base == "dist" {
				return fs.SkipDir
			}
			return nil
		}
		inv.files[path.Clean(name)] = struct{}{}
		return nil
	}); err != nil {
		return Report{}, fmt.Errorf("walk repository: %w", err)
	}

	checks := []Check{
		dependencyCheck(inv),
		verificationCheck(repo, inv),
		environmentCheck(repo, inv),
		ciCheck(inv),
		ownershipCheck(inv),
		documentationCheck(inv),
		agentGuidanceCheck(inv),
	}

	report := Report{Status: StatusReady, Checks: checks}
	for _, check := range checks {
		if check.Critical && check.Status == StatusBlocked {
			report.Status = StatusBlocked
			return report, nil
		}
		if check.Status != StatusReady {
			report.Status = StatusAttention
		}
	}
	return report, nil
}

func dependencyCheck(inv inventory) Check {
	check := Check{ID: "dependencies", Title: "Dependency reproducibility", Critical: true}
	manifests := inv.matchBase("package.json", "go.mod", "Cargo.toml", "pyproject.toml", "requirements.txt", "Pipfile")
	if len(manifests) == 0 {
		check.Status = StatusAttention
		check.Recommendation = "Declare dependency metadata so agent workspaces can be reproduced."
		return check
	}

	check.Evidence = append(check.Evidence, manifests...)
	nodeManifests := inv.matchBase("package.json")
	if len(nodeManifests) > 0 {
		nodeLocks := inv.matchBase("pnpm-lock.yaml", "package-lock.json", "npm-shrinkwrap.json", "yarn.lock", "bun.lock", "bun.lockb")
		if len(nodeLocks) == 0 {
			check.Status = StatusBlocked
			check.Recommendation = "Commit a Node package-manager lockfile before autonomous execution."
			return check
		}
		check.Evidence = append(check.Evidence, nodeLocks...)
	}

	softMissing := false
	if len(inv.matchBase("Cargo.toml")) > 0 {
		locks := inv.matchBase("Cargo.lock")
		if len(locks) == 0 {
			softMissing = true
		} else {
			check.Evidence = append(check.Evidence, locks...)
		}
	}
	if len(inv.matchBase("pyproject.toml", "Pipfile")) > 0 {
		locks := inv.matchBase("uv.lock", "poetry.lock", "Pipfile.lock")
		if len(locks) == 0 {
			softMissing = true
		} else {
			check.Evidence = append(check.Evidence, locks...)
		}
	}
	if len(inv.matchBase("go.mod")) > 0 {
		check.Evidence = append(check.Evidence, inv.matchBase("go.sum")...)
	}

	check.Evidence = uniqueSorted(check.Evidence)
	if softMissing {
		check.Status = StatusAttention
		check.Recommendation = "Commit ecosystem lockfiles where the repository is an executable application."
		return check
	}
	check.Status = StatusReady
	return check
}

func verificationCheck(repo fs.FS, inv inventory) Check {
	check := Check{ID: "verification", Title: "Deterministic verification", Critical: true}
	commands := make([]string, 0, 6)

	for _, makefile := range inv.matchBase("Makefile") {
		data, err := fs.ReadFile(repo, makefile)
		if err != nil {
			continue
		}
		targets := makeTargets(string(data))
		for _, target := range []string{"test", "lint", "typecheck", "build"} {
			if targets[target] {
				commands = append(commands, "make "+target)
			}
		}
	}

	for _, packageFile := range inv.matchBase("package.json") {
		data, err := fs.ReadFile(repo, packageFile)
		if err != nil {
			continue
		}
		var manifest struct {
			Scripts map[string]string `json:"scripts"`
		}
		if json.Unmarshal(data, &manifest) != nil {
			continue
		}
		dir := path.Dir(packageFile)
		prefix := ""
		if dir != "." {
			prefix = dir + ": "
		}
		for _, script := range []string{"test", "lint", "typecheck", "build"} {
			if strings.TrimSpace(manifest.Scripts[script]) != "" {
				commands = append(commands, prefix+"npm run "+script)
			}
		}
	}

	commands = uniqueSorted(commands)
	check.Evidence = commands
	hasTest := containsCommand(commands, "test")
	hasSecondary := containsCommand(commands, "lint") || containsCommand(commands, "typecheck") || containsCommand(commands, "build")
	if !hasTest {
		check.Status = StatusBlocked
		check.Recommendation = "Expose a deterministic test command through the repository build interface or package scripts."
		return check
	}
	if !hasSecondary {
		check.Status = StatusAttention
		check.Recommendation = "Add lint, typecheck, or build verification in addition to tests."
		return check
	}
	check.Status = StatusReady
	return check
}

func environmentCheck(repo fs.FS, inv inventory) Check {
	check := Check{ID: "environment", Title: "Environment reproducibility"}
	evidence := inv.matchBase("Dockerfile", "docker-compose.yml", "docker-compose.yaml", "compose.yml", "compose.yaml", "flake.nix", "mise.toml", ".tool-versions", "devcontainer.json")
	if inv.has(".devcontainer/devcontainer.json") {
		evidence = append(evidence, ".devcontainer/devcontainer.json")
	}
	if inv.has("go.work") {
		if data, err := fs.ReadFile(repo, "go.work"); err == nil && strings.Contains(string(data), "toolchain ") {
			evidence = append(evidence, "go.work toolchain")
		}
	}
	check.Evidence = uniqueSorted(evidence)
	if len(check.Evidence) == 0 {
		check.Status = StatusAttention
		check.Recommendation = "Pin the development/runtime environment with a container, toolchain file, or equivalent."
		return check
	}
	check.Status = StatusReady
	return check
}

func ciCheck(inv inventory) Check {
	check := Check{ID: "ci", Title: "Continuous integration"}
	evidence := make([]string, 0)
	for name := range inv.files {
		if (strings.HasPrefix(name, ".github/workflows/") && (strings.HasSuffix(name, ".yml") || strings.HasSuffix(name, ".yaml"))) ||
			name == ".woodpecker.yml" || name == ".woodpecker.yaml" || name == ".gitlab-ci.yml" ||
			name == "Jenkinsfile" || name == ".circleci/config.yml" {
			evidence = append(evidence, name)
		}
	}
	check.Evidence = uniqueSorted(evidence)
	if len(check.Evidence) == 0 {
		check.Status = StatusAttention
		check.Recommendation = "Add CI that reruns the same deterministic verification used by agents."
		return check
	}
	check.Status = StatusReady
	return check
}

func ownershipCheck(inv inventory) Check {
	check := Check{ID: "ownership", Title: "Code ownership boundaries"}
	evidence := inv.matchBase("CODEOWNERS")
	check.Evidence = evidence
	if len(evidence) == 0 {
		check.Status = StatusAttention
		check.Recommendation = "Add CODEOWNERS or another explicit ownership map for approval routing."
		return check
	}
	check.Status = StatusReady
	return check
}

func documentationCheck(inv inventory) Check {
	check := Check{ID: "documentation", Title: "Repository documentation"}
	evidence := inv.matchBase("README.md", "CONTRIBUTING.md")
	check.Evidence = evidence
	hasReadme := len(inv.matchBase("README.md")) > 0
	hasContributing := len(inv.matchBase("CONTRIBUTING.md")) > 0
	if hasReadme && hasContributing {
		check.Status = StatusReady
		return check
	}
	check.Status = StatusAttention
	check.Recommendation = "Document repository setup and contribution/verification conventions."
	return check
}

func agentGuidanceCheck(inv inventory) Check {
	check := Check{ID: "agent_guidance", Title: "Agent guidance"}
	evidence := inv.matchBase("AGENTS.md", "CLAUDE.md")
	check.Evidence = evidence
	if len(evidence) == 0 {
		check.Status = StatusAttention
		check.Recommendation = "Add concise agent instructions for repository-specific constraints and verification."
		return check
	}
	check.Status = StatusReady
	return check
}

func (inv inventory) has(name string) bool {
	_, ok := inv.files[path.Clean(name)]
	return ok
}

func (inv inventory) matchBase(names ...string) []string {
	wanted := make(map[string]struct{}, len(names))
	for _, name := range names {
		wanted[name] = struct{}{}
	}
	matches := make([]string, 0)
	for name := range inv.files {
		if _, ok := wanted[path.Base(name)]; ok {
			matches = append(matches, name)
		}
	}
	sort.Strings(matches)
	return matches
}

func makeTargets(content string) map[string]bool {
	result := map[string]bool{}
	for _, line := range strings.Split(content, "\n") {
		if line == "" || line[0] == '\t' || strings.HasPrefix(strings.TrimSpace(line), "#") {
			continue
		}
		idx := strings.IndexByte(line, ':')
		if idx <= 0 {
			continue
		}
		for _, target := range strings.Fields(line[:idx]) {
			result[target] = true
		}
	}
	return result
}

func containsCommand(commands []string, token string) bool {
	needle := " " + token
	for _, command := range commands {
		if strings.Contains(command, needle) || strings.HasSuffix(command, token) {
			return true
		}
	}
	return false
}

func uniqueSorted(values []string) []string {
	seen := make(map[string]struct{}, len(values))
	result := make([]string, 0, len(values))
	for _, value := range values {
		if value == "" {
			continue
		}
		if _, ok := seen[value]; ok {
			continue
		}
		seen[value] = struct{}{}
		result = append(result, value)
	}
	sort.Strings(result)
	return result
}
