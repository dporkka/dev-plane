package main

import (
	"bufio"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/ai-dev-control-plane/cli/internal/acp"
)

type stringList []string

func (s *stringList) String() string     { return strings.Join(*s, ",") }
func (s *stringList) Set(v string) error { *s = append(*s, v); return nil }

type sessionState struct {
	Version   int       `json:"version"`
	Agent     string    `json:"agent"`
	SessionID string    `json:"sessionId"`
	CWD       string    `json:"cwd"`
	TaskID    string    `json:"taskId,omitempty"`
	RunID     string    `json:"runId,omitempty"`
	StartedAt time.Time `json:"startedAt"`
}

type commandOptions struct {
	Agent         string
	AgentExec     string
	AgentArgs     []string
	CWD           string
	TaskID        string
	RunID         string
	Permission    acp.PermissionMode
	AuthMethod    string
	ResumeSession string
	StateDir      string
	Thoughts      bool
	Verbose       bool
}

func main() {
	if err := run(context.Background(), os.Args[1:], os.Stdin, os.Stdout, os.Stderr); err != nil {
		fmt.Fprintf(os.Stderr, "devx: %v\n", err)
		os.Exit(1)
	}
}

func run(ctx context.Context, args []string, stdin io.Reader, stdout, stderr io.Writer) error {
	if len(args) == 0 || args[0] == "help" || args[0] == "--help" || args[0] == "-h" {
		printUsage(stdout)
		return nil
	}
	switch args[0] {
	case "ask":
		return runAsk(ctx, args[1:], stdin, stdout, stderr)
	case "chat":
		return runChat(ctx, args[1:], stdin, stdout, stderr)
	default:
		return fmt.Errorf("unknown command %q", args[0])
	}
}

func runAsk(ctx context.Context, args []string, stdin io.Reader, stdout, stderr io.Writer) error {
	opts, rest, err := parseCommon("ask", args)
	if err != nil {
		return err
	}
	if len(rest) == 0 {
		return errors.New("usage: devx ask [options] <prompt>")
	}
	prompt := strings.Join(rest, " ")
	return withSession(ctx, opts, stdin, stdout, stderr, func(client *acp.Client, sessionID string, meta acp.SessionMetadata) error {
		result, err := client.Prompt(ctx, sessionID, prompt, meta)
		if err != nil {
			return err
		}
		if opts.Verbose {
			fmt.Fprintf(stderr, "\n[stop: %s]\n", result.StopReason)
		}
		return nil
	})
}

func runChat(ctx context.Context, args []string, stdin io.Reader, stdout, stderr io.Writer) error {
	opts, rest, err := parseCommon("chat", args)
	if err != nil {
		return err
	}
	if len(rest) != 0 {
		return fmt.Errorf("unexpected arguments: %s", strings.Join(rest, " "))
	}
	reader := bufio.NewReader(stdin)
	return withSession(ctx, opts, reader, stdout, stderr, func(client *acp.Client, sessionID string, meta acp.SessionMetadata) error {
		fmt.Fprintf(stderr, "devx chat · %s · session %s\n", opts.Agent, sessionID)
		fmt.Fprintln(stderr, "Commands: :quit, :cancel, :help")
		for {
			fmt.Fprint(stderr, "> ")
			line, err := reader.ReadString('\n')
			if err != nil && !errors.Is(err, io.EOF) {
				return err
			}
			line = strings.TrimSpace(line)
			if line == "" && errors.Is(err, io.EOF) {
				return nil
			}
			if line == "" {
				continue
			}
			switch line {
			case ":quit", ":q", ":exit":
				return nil
			case ":help":
				fmt.Fprintln(stderr, "Commands: :quit, :cancel, :help")
				continue
			case ":cancel":
				if cancelErr := client.Cancel(sessionID); cancelErr != nil {
					fmt.Fprintf(stderr, "cancel: %v\n", cancelErr)
				}
				continue
			}
			result, promptErr := client.Prompt(ctx, sessionID, line, meta)
			if promptErr != nil {
				return promptErr
			}
			if !strings.HasSuffix(line, "\n") {
				fmt.Fprintln(stdout)
			}
			if opts.Verbose {
				fmt.Fprintf(stderr, "[stop: %s]\n", result.StopReason)
			}
			if errors.Is(err, io.EOF) {
				return nil
			}
		}
	})
}

func parseCommon(name string, args []string) (commandOptions, []string, error) {
	fs := flag.NewFlagSet(name, flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	var agentArgs stringList
	agent := fs.String("agent", "codex", "ACP agent: codex or gemini")
	agentExec := fs.String("agent-exec", "", "override ACP executable")
	fs.Var(&agentArgs, "agent-arg", "argument for --agent-exec (repeatable)")
	cwd := fs.String("cwd", ".", "session working directory")
	taskID := fs.String("task-id", os.Getenv("DEV_PLANE_TASK_ID"), "Dev Plane task ID")
	runID := fs.String("run-id", os.Getenv("DEV_PLANE_RUN_ID"), "Dev Plane run ID")
	permission := fs.String("permission", "ask", "permission mode: ask, allow, deny")
	authMethod := fs.String("auth-method", "", "ACP protocol auth method ID, e.g. chat-gpt or api-key")
	resume := fs.String("resume-session", "", "load an existing ACP v1 session")
	stateDir := fs.String("state-dir", defaultStateDir(), "directory for devx session metadata")
	thoughts := fs.Bool("thoughts", false, "render agent thought chunks")
	verbose := fs.Bool("verbose", false, "render protocol lifecycle details")
	if err := fs.Parse(args); err != nil {
		return commandOptions{}, nil, err
	}
	mode := acp.PermissionMode(*permission)
	if mode != acp.PermissionAsk && mode != acp.PermissionAllow && mode != acp.PermissionDeny {
		return commandOptions{}, nil, fmt.Errorf("--permission must be ask, allow, or deny")
	}
	return commandOptions{
		Agent: *agent, AgentExec: *agentExec, AgentArgs: agentArgs, CWD: *cwd,
		TaskID: *taskID, RunID: *runID, Permission: mode, AuthMethod: *authMethod, ResumeSession: *resume,
		StateDir: *stateDir, Thoughts: *thoughts, Verbose: *verbose,
	}, fs.Args(), nil
}

func withSession(ctx context.Context, opts commandOptions, stdin io.Reader, stdout, stderr io.Writer, fn func(*acp.Client, string, acp.SessionMetadata) error) error {
	spec, err := resolveAgent(opts.Agent, opts.AgentExec, opts.AgentArgs)
	if err != nil {
		return err
	}
	absCWD, err := filepath.Abs(opts.CWD)
	if err != nil {
		return fmt.Errorf("resolve cwd: %w", err)
	}
	info, err := os.Stat(absCWD)
	if err != nil {
		return fmt.Errorf("cwd: %w", err)
	}
	if !info.IsDir() {
		return fmt.Errorf("cwd is not a directory: %s", absCWD)
	}
	spec.Dir = absCWD

	decider := permissionDecider(opts.Permission, stdin, stderr)
	client, err := acp.Start(ctx, spec, acp.Options{
		Stderr:           stderr,
		DecidePermission: decider,
		OnUpdate:         updateRenderer(stdout, stderr, opts.Thoughts, opts.Verbose),
	})
	if err != nil {
		return err
	}
	defer client.Close()

	init, err := client.Initialize(ctx)
	if err != nil {
		return err
	}
	if opts.Verbose {
		fmt.Fprintf(stderr, "[ACP v%d · %s %s]\n", init.ProtocolVersion, init.AgentName, init.AgentVersion)
		if len(init.AuthMethods) > 0 {
			fmt.Fprint(stderr, "[auth methods:")
			for _, method := range init.AuthMethods {
				fmt.Fprintf(stderr, " %s", method.ID)
			}
			fmt.Fprintln(stderr, "]")
		}
	}
	if opts.AuthMethod != "" {
		if err := client.Authenticate(ctx, opts.AuthMethod); err != nil {
			return fmt.Errorf("authenticate with %s: %w", opts.AuthMethod, err)
		}
	}

	meta := acp.SessionMetadata{TaskID: opts.TaskID, RunID: opts.RunID}
	var sessionID string
	if opts.ResumeSession != "" {
		if err := client.LoadSession(ctx, opts.ResumeSession, absCWD, meta); err != nil {
			return err
		}
		sessionID = opts.ResumeSession
	} else {
		session, err := client.NewSession(ctx, absCWD, meta)
		if err != nil {
			return err
		}
		sessionID = session.ID
	}

	state := sessionState{Version: 1, Agent: opts.Agent, SessionID: sessionID, CWD: absCWD, TaskID: opts.TaskID, RunID: opts.RunID, StartedAt: time.Now().UTC()}
	statePath, stateErr := saveSessionState(opts.StateDir, state)
	if stateErr != nil {
		fmt.Fprintf(stderr, "warning: save session metadata: %v\n", stateErr)
	} else if opts.Verbose {
		fmt.Fprintf(stderr, "[state: %s]\n", statePath)
	}

	return fn(client, sessionID, meta)
}

func resolveAgent(name, override string, overrideArgs []string) (acp.AgentSpec, error) {
	if override != "" {
		return acp.AgentSpec{Command: override, Args: append([]string(nil), overrideArgs...)}, nil
	}
	switch name {
	case "codex":
		return acp.AgentSpec{Command: "npx", Args: []string{"-y", "@agentclientprotocol/codex-acp@2.1.1"}}, nil
	case "gemini":
		return acp.AgentSpec{Command: "gemini", Args: []string{"--acp"}}, nil
	default:
		return acp.AgentSpec{}, fmt.Errorf("unknown agent %q; use --agent-exec for a custom ACP agent", name)
	}
}

func permissionDecider(mode acp.PermissionMode, input io.Reader, output io.Writer) func(acp.PermissionRequest) acp.PermissionDecision {
	if mode == acp.PermissionAllow || mode == acp.PermissionDeny {
		return func(req acp.PermissionRequest) acp.PermissionDecision { return acp.ChoosePermission(mode, req.Options) }
	}
	reader, ok := input.(*bufio.Reader)
	if !ok {
		reader = bufio.NewReader(input)
	}
	return func(req acp.PermissionRequest) acp.PermissionDecision {
		title := req.ToolCall.Title
		if title == "" {
			title = req.ToolCall.Name
		}
		if title == "" {
			title = "Agent operation"
		}
		fmt.Fprintf(output, "\nPermission requested: %s\n", title)
		for i, option := range req.Options {
			fmt.Fprintf(output, "  %d) %s [%s]\n", i+1, option.Name, option.Kind)
		}
		fallback := acp.ChoosePermission(acp.PermissionDeny, req.Options)
		fmt.Fprint(output, "Choose option (blank = deny): ")
		line, err := reader.ReadString('\n')
		if err != nil && !errors.Is(err, io.EOF) {
			return fallback
		}
		line = strings.TrimSpace(line)
		if line == "" {
			return fallback
		}
		n, err := strconv.Atoi(line)
		if err != nil || n < 1 || n > len(req.Options) {
			return fallback
		}
		return acp.PermissionDecision{OptionID: req.Options[n-1].OptionID}
	}
}

func updateRenderer(stdout, stderr io.Writer, thoughts, verbose bool) func(acp.Update) {
	return func(update acp.Update) {
		switch update.Kind {
		case "agent_message_chunk":
			if update.Text != "" {
				fmt.Fprint(stdout, update.Text)
			}
		case "agent_thought_chunk":
			if thoughts && update.Text != "" {
				fmt.Fprintf(stderr, "[thought] %s", update.Text)
			}
		case "tool_call":
			title := update.ToolCall.Title
			if title == "" {
				title = update.ToolCall.Name
			}
			if title != "" {
				fmt.Fprintf(stderr, "\n→ %s\n", title)
			}
		case "tool_call_update":
			if update.ToolCall.Title != "" || update.ToolCall.Status != "" {
				fmt.Fprintf(stderr, "↳ %s %s\n", update.ToolCall.Status, update.ToolCall.Title)
			}
		default:
			if verbose && update.Kind != "user_message_chunk" {
				fmt.Fprintf(stderr, "[%s]\n", update.Kind)
			}
		}
	}
}

func saveSessionState(dir string, state sessionState) (string, error) {
	if state.Version == 0 {
		state.Version = 1
	}
	if state.StartedAt.IsZero() {
		state.StartedAt = time.Now().UTC()
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", err
	}
	sum := sha256.Sum256([]byte(state.Agent + "\x00" + state.CWD + "\x00" + state.SessionID))
	name := hex.EncodeToString(sum[:8]) + ".json"
	path := filepath.Join(dir, name)
	data, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		return "", err
	}
	data = append(data, '\n')
	if err := os.WriteFile(path, data, 0o600); err != nil {
		return "", err
	}
	return path, nil
}

func defaultStateDir() string {
	if value := os.Getenv("XDG_STATE_HOME"); value != "" {
		return filepath.Join(value, "devx", "sessions")
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return filepath.Join(".", ".devx-state")
	}
	return filepath.Join(home, ".local", "state", "devx", "sessions")
}

func printUsage(w io.Writer) {
	fmt.Fprintln(w, `devx — thin ACP client for Dev Plane workflows

Usage:
  devx ask  [options] <prompt>
  devx chat [options]

Options:
  --agent codex|gemini        ACP agent (default: codex)
  --cwd PATH                  session workspace (default: .)
  --task-id ID                attach Dev Plane task ID (or DEV_PLANE_TASK_ID)
  --run-id ID                 attach Dev Plane run ID (or DEV_PLANE_RUN_ID)
  --permission ask|allow|deny permission policy (default: ask)
  --auth-method ID             ACP protocol auth method (e.g. chat-gpt)
  --resume-session ID         load an existing ACP v1 session when supported
  --thoughts                   render agent thought chunks
  --verbose                    show lifecycle details
  --agent-exec PATH            custom ACP executable
  --agent-arg ARG              custom ACP executable argument (repeatable)

Examples:
  devx chat --agent codex --auth-method chat-gpt --cwd ~/projects/adacavo --task-id task_123
  devx ask --agent gemini --permission deny "review the current diff"
  devx chat --agent codex --resume-session sess_abc123`)
}
