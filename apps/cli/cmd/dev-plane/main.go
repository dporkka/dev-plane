package main

import (
	"fmt"
	"os"
)

func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}
}

func run(args []string) error {
	if len(args) == 0 {
		printUsage()
		return nil
	}

	switch args[0] {
	case "login":
		return runLogin(args[1:])
	case "tasks":
		if len(args) < 2 {
			return fmt.Errorf("usage: dev-plane tasks <list|get|create>")
		}
		switch args[1] {
		case "list":
			return runTasksList(args[2:])
		case "get":
			return runTasksGet(args[2:])
		case "create":
			return runTasksCreate(args[2:])
		default:
			return fmt.Errorf("unknown tasks subcommand: %s", args[1])
		}
	case "runs":
		if len(args) < 2 {
			return fmt.Errorf("usage: dev-plane runs <list|logs>")
		}
		switch args[1] {
		case "list":
			return runRunsList(args[2:])
		case "logs":
			return runRunsLogs(args[2:])
		default:
			return fmt.Errorf("unknown runs subcommand: %s", args[1])
		}
	case "approvals":
		if len(args) < 2 {
			return fmt.Errorf("usage: dev-plane approvals <list|respond>")
		}
		switch args[1] {
		case "list":
			return runApprovalsList(args[2:])
		case "respond":
			return runApprovalsRespond(args[2:])
		default:
			return fmt.Errorf("unknown approvals subcommand: %s", args[1])
		}
	case "help", "--help", "-h":
		printUsage()
		return nil
	default:
		return fmt.Errorf("unknown command: %s", args[0])
	}
}

func printUsage() {
	fmt.Println(`dev-plane CLI

Usage:
  dev-plane login --base-url=<url>
  dev-plane tasks list --project-id=<id>
  dev-plane tasks get <id>
  dev-plane tasks create --project-id=<id> --repository-id=<id> --title=<title> [--description=<desc>]
  dev-plane runs list --task-id=<id>
  dev-plane runs logs <id>
  dev-plane approvals list --org-id=<id>
  dev-plane approvals respond --response=approved|rejected <id>
  dev-plane help

Configuration is stored in ~/.config/dev-plane/config.json.`)
}
