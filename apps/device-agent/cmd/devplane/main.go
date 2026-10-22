//go:build linux

package main

import (
	"context"
	"flag"
	"fmt"
	"github.com/ai-dev-control-plane/device-agent/internal/devplane"
	"os"
)

func main() {
	workspace := flag.String("workspace", "", "Existing workspace directory to authorize. Required.")
	flag.Parse()
	if *workspace == "" {
		fmt.Fprintln(os.Stderr, "--workspace is required; no default home directory access")
		os.Exit(2)
	}
	server, err := devplane.NewServer(*workspace)
	if err != nil {
		fmt.Fprintln(os.Stderr, "workspace error:", err)
		os.Exit(1)
	}
	defer server.Close()
	if err := server.ServeStdio(context.Background(), os.Stdin, os.Stdout); err != nil {
		fmt.Fprintln(os.Stderr, "mcp error:", err)
		os.Exit(1)
	}
}
