package main

import (
	"flag"
	"fmt"
	"os"
	"strings"

	verification "github.com/ai-dev-control-plane/verification"
)

func runEvidenceVerify(args []string) error {
	fs := flag.NewFlagSet("evidence verify", flag.ContinueOnError)
	repositoryPath := fs.String("path", ".", "repository root whose HEAD tree must match the evidence")
	environmentDigest := fs.String("environment-digest", os.Getenv("DEV_PLANE_ENVIRONMENT_DIGEST"), "expected verification environment digest")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if len(fs.Args()) != 1 {
		return fmt.Errorf("usage: dev-plane evidence verify [--path=.] [--environment-digest=<digest>] <artifact>")
	}
	if strings.TrimSpace(*environmentDigest) == "" {
		return fmt.Errorf("--environment-digest or DEV_PLANE_ENVIRONMENT_DIGEST is required")
	}
	if err := requireCleanCommittedCandidate(*repositoryPath); err != nil {
		return err
	}

	data, err := os.ReadFile(fs.Args()[0])
	if err != nil {
		return fmt.Errorf("read verification artifact: %w", err)
	}
	artifact, err := verification.ParseArtifact(data)
	if err != nil {
		return fmt.Errorf("parse verification artifact: %w", err)
	}

	treeHash, err := gitTreeHash(*repositoryPath)
	if err != nil {
		return err
	}
	fresh, reason, err := artifact.FreshFor(treeHash, strings.TrimSpace(*environmentDigest))
	if err != nil {
		return fmt.Errorf("verify evidence freshness: %w", err)
	}
	if !fresh {
		return fmt.Errorf("verification evidence is stale: %s", reason)
	}

	fmt.Printf(
		"verification evidence valid: tree=%s runner=%s checks=%d\n",
		artifact.Evidence.TreeHash,
		artifact.Evidence.RunnerIdentity,
		len(artifact.Evidence.Checks),
	)
	return nil
}
