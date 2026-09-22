package repogate

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	"github.com/ai-dev-control-plane/runtimes"
	"github.com/ai-dev-control-plane/vcs"
)

type commandVerifier struct {
	runner   vcs.CommandRunner
	commands [][]string
}

func newVerifier(runner vcs.CommandRunner, configured []string) (*commandVerifier, error) {
	if runner == nil {
		return nil, fmt.Errorf("verification command runner is required")
	}
	commands := make([][]string, 0, len(configured)+1)
	commands = append(commands, []string{"git", "diff", "--check", "HEAD^", "HEAD", "--"})
	for _, command := range configured {
		args, err := runtimes.ParseCommandString(command)
		if err != nil {
			return nil, fmt.Errorf("invalid project verification command %q: %w", command, err)
		}
		if len(args) != 0 {
			commands = append(commands, args)
		}
	}
	return &commandVerifier{runner: runner, commands: commands}, nil
}

func (v *commandVerifier) Verify(ctx context.Context, workspacePath string) (vcs.VerificationReport, error) {
	evidence := map[string]string{"checks": strconv.Itoa(len(v.commands))}
	for i, args := range v.commands {
		result, err := v.runner.Run(ctx, vcs.Command{Name: args[0], Args: args[1:], Dir: workspacePath})
		key := fmt.Sprintf("check_%02d", i+1)
		evidence[key] = strings.Join(args, " ")
		if err != nil {
			evidence[key+"_result"] = "failed"
			if output := strings.TrimSpace(result.Stderr); output != "" {
				evidence[key+"_stderr"] = truncateEvidence(output)
			}
			return vcs.VerificationReport{Passed: false, Evidence: evidence}, nil
		}
		evidence[key+"_result"] = "passed"
	}
	return vcs.VerificationReport{Passed: true, Evidence: evidence}, nil
}

func truncateEvidence(value string) string {
	const max = 2048
	if len(value) <= max {
		return value
	}
	return value[:max] + "..."
}
