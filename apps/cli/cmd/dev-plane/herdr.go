package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"regexp"
	"sort"
	"strings"
	"text/tabwriter"
	"time"
)

// This local-only adapter intentionally exports no cwd, terminal titles,
// session paths, scrollback, prompts, or raw agent output.
type herdrAgent struct {
	Agent       string `json:"agent"`
	Status      string `json:"status"`
	PaneID      string `json:"pane_id"`
	WorkspaceID string `json:"workspace_id"`
	Focused     bool   `json:"focused"`
}
type herdrAgentInventory struct {
	Source   string       `json:"source"`
	Version  string       `json:"version"`
	Protocol int          `json:"protocol"`
	Total    int          `json:"total"`
	Agents   []herdrAgent `json:"agents"`
}

var herdrToken = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9_:.@/-]{0,127}$`)
var herdrStatusToken = regexp.MustCompile(`^[a-z][a-z0-9_-]{0,39}$`)

func parseHerdrSnapshot(data []byte) (herdrAgentInventory, error) {
	var envelope struct {
		Result *struct {
			Type     string `json:"type"`
			Snapshot *struct {
				Version  string `json:"version"`
				Protocol int    `json:"protocol"`
				Agents   []struct {
					Agent       string `json:"agent"`
					Status      string `json:"agent_status"`
					PaneID      string `json:"pane_id"`
					WorkspaceID string `json:"workspace_id"`
					Focused     bool   `json:"focused"`
				} `json:"agents"`
			} `json:"snapshot"`
		} `json:"result"`
	}
	if len(data) > 4<<20 {
		return herdrAgentInventory{}, errors.New("herdr snapshot exceeds safety limit")
	}
	if err := json.Unmarshal(data, &envelope); err != nil {
		return herdrAgentInventory{}, errors.New("invalid Herdr snapshot JSON")
	}
	if envelope.Result == nil || envelope.Result.Type != "session_snapshot" ||
		envelope.Result.Snapshot == nil || envelope.Result.Snapshot.Protocol <= 0 ||
		strings.TrimSpace(envelope.Result.Snapshot.Version) == "" ||
		envelope.Result.Snapshot.Agents == nil {
		return herdrAgentInventory{}, errors.New("missing or unsupported Herdr session snapshot")
	}

	snapshot := envelope.Result.Snapshot
	out := herdrAgentInventory{
		Source:   "herdr",
		Version:  snapshot.Version,
		Protocol: snapshot.Protocol,
		Agents:   make([]herdrAgent, 0, len(snapshot.Agents)),
	}
	seen := make(map[string]bool, len(snapshot.Agents))
	for _, a := range snapshot.Agents {
		if !herdrToken.MatchString(a.Agent) || !herdrStatusToken.MatchString(a.Status) ||
			!herdrToken.MatchString(a.PaneID) || !herdrToken.MatchString(a.WorkspaceID) ||
			seen[a.PaneID] {
			return herdrAgentInventory{}, errors.New("invalid or duplicate Herdr agent metadata")
		}
		seen[a.PaneID] = true
		out.Agents = append(out.Agents, herdrAgent{
			Agent: a.Agent, Status: a.Status, PaneID: a.PaneID,
			WorkspaceID: a.WorkspaceID, Focused: a.Focused,
		})
	}
	sort.Slice(out.Agents, func(i, j int) bool {
		return out.Agents[i].PaneID < out.Agents[j].PaneID
	})
	out.Total = len(out.Agents)
	return out, nil
}

const herdrMaxSnapshotBytes = 4 << 20

// herdrSnapshotCapture never retains more than the limit plus one sentinel
// byte. On overflow it cancels the child instead of buffering untrusted output.
type herdrSnapshotCapture struct {
	buf       bytes.Buffer
	cancel    context.CancelFunc
	overLimit bool
}

func (c *herdrSnapshotCapture) Write(p []byte) (int, error) {
	if c.overLimit {
		return len(p), nil
	}
	remaining := herdrMaxSnapshotBytes + 1 - c.buf.Len()
	if remaining < 0 {
		remaining = 0
	}
	if len(p) < remaining {
		remaining = len(p)
	}
	if _, err := c.buf.Write(p[:remaining]); err != nil {
		return 0, err
	}
	if c.buf.Len() > herdrMaxSnapshotBytes {
		c.overLimit = true
		if c.cancel != nil {
			c.cancel()
		}
	}
	return len(p), nil
}

func runAgentsLocal(args []string) error {
	fs := flag.NewFlagSet("agents local", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	jsonOutput := fs.Bool("json", false, "machine-readable JSON")
	if err := fs.Parse(args); err != nil {
		return fmt.Errorf("invalid agents local arguments: %w", err)
	}
	if fs.NArg() != 0 {
		return errors.New("agents local takes no positional arguments")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	// Command arguments are fixed. Never forward caller-controlled shell input.
	capture := &herdrSnapshotCapture{cancel: cancel}
	cmd := exec.CommandContext(ctx, "herdr", "api", "snapshot")
	cmd.WaitDelay = time.Second
	cmd.Stdout = capture
	err := cmd.Run()
	if capture.overLimit {
		return errors.New("herdr snapshot exceeds safety limit")
	}
	if err != nil {
		return errors.New("unable to read local Herdr snapshot (check that Herdr is installed and running)")
	}
	inventory, err := parseHerdrSnapshot(capture.buf.Bytes())
	if err != nil {
		return err
	}
	if *jsonOutput {
		return printJSON(inventory)
	}

	w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	if _, err := fmt.Fprintln(w, "PANE\tAGENT\tSTATUS\tFOCUSED\tWORKSPACE"); err != nil {
		return fmt.Errorf("write agent table header: %w", err)
	}
	for _, a := range inventory.Agents {
		if _, err := fmt.Fprintf(w, "%s\t%s\t%s\t%t\t%s\n", a.PaneID, a.Agent, a.Status, a.Focused, a.WorkspaceID); err != nil {
			return fmt.Errorf("write agent table row: %w", err)
		}
	}
	return w.Flush()
}
