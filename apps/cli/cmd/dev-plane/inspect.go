package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/ai-dev-control-plane/readiness"
)

func runInspect(args []string) error {
	return runInspectTo(args, os.Stdout)
}

func runInspectTo(args []string, out io.Writer) error {
	flags := flag.NewFlagSet("inspect", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	repoPath := flags.String("path", ".", "repository path")
	jsonOutput := flags.Bool("json", false, "emit JSON")
	if err := flags.Parse(args); err != nil {
		return fmt.Errorf("usage: dev-plane inspect [--path=<repo>] [--json]: %w", err)
	}
	if flags.NArg() != 0 {
		return fmt.Errorf("usage: dev-plane inspect [--path=<repo>] [--json]")
	}

	report, err := readiness.Inspect(os.DirFS(*repoPath))
	if err != nil {
		return fmt.Errorf("inspect repository: %w", err)
	}
	if *jsonOutput {
		encoder := json.NewEncoder(out)
		encoder.SetIndent("", "  ")
		return encoder.Encode(report)
	}

	fmt.Fprintf(out, "Repository readiness: %s\n", strings.ToUpper(string(report.Status)))
	for _, check := range report.Checks {
		critical := ""
		if check.Critical {
			critical = " critical"
		}
		fmt.Fprintf(out, "[%s]%s %s\n", strings.ToUpper(string(check.Status)), critical, check.Title)
		for _, evidence := range check.Evidence {
			fmt.Fprintf(out, "  evidence: %s\n", evidence)
		}
		if check.Recommendation != "" {
			fmt.Fprintf(out, "  next: %s\n", check.Recommendation)
		}
	}
	return nil
}
