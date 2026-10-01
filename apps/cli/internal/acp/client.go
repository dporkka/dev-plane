package acp

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"sync"
	"sync/atomic"
)

type AgentSpec struct {
	Command string
	Args    []string
	Env     []string
	Dir     string
}

type PermissionMode string

const (
	PermissionAsk   PermissionMode = "ask"
	PermissionAllow PermissionMode = "allow"
	PermissionDeny  PermissionMode = "deny"
)

type PermissionOption struct {
	OptionID string `json:"optionId"`
	Name     string `json:"name"`
	Kind     string `json:"kind"`
}

type ToolCall struct {
	ToolCallID string         `json:"toolCallId"`
	Title      string         `json:"title"`
	Name       string         `json:"name"`
	Kind       string         `json:"kind"`
	Status     string         `json:"status"`
	RawInput   any            `json:"rawInput"`
	RawOutput  any            `json:"rawOutput"`
	Meta       map[string]any `json:"_meta"`
}

type PermissionRequest struct {
	SessionID string             `json:"sessionId"`
	ToolCall  ToolCall           `json:"toolCall"`
	Options   []PermissionOption `json:"options"`
	Meta      map[string]any     `json:"_meta"`
}

type PermissionDecision struct {
	OptionID string
	Cancel   bool
}

type Update struct {
	SessionID string
	Kind      string
	Text      string
	ToolCall  ToolCall
	Raw       map[string]any
}

type SessionMetadata struct {
	TaskID string
	RunID  string
}

type InitializeResult struct {
	ProtocolVersion int
	LoadSession     bool
	AgentName       string
	AgentVersion    string
	RawCapabilities map[string]any
}

type Session struct{ ID string }
type PromptResult struct{ StopReason string }

type Options struct {
	OnUpdate         func(Update)
	DecidePermission func(PermissionRequest) PermissionDecision
	Stderr           io.Writer
}

type rpcResponse struct {
	Result json.RawMessage
	Error  *rpcError
}

type rpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

type wireMessage struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Method  string          `json:"method,omitempty"`
	Params  json.RawMessage `json:"params,omitempty"`
	Result  json.RawMessage `json:"result,omitempty"`
	Error   *rpcError       `json:"error,omitempty"`
}

type Client struct {
	cmd   *exec.Cmd
	stdin io.WriteCloser

	writeMu sync.Mutex
	nextID  atomic.Int64

	pendingMu sync.Mutex
	pending   map[string]chan rpcResponse

	done    chan struct{}
	errMu   sync.Mutex
	readErr error

	opts Options
	init InitializeResult
}

func Start(ctx context.Context, spec AgentSpec, opts Options) (*Client, error) {
	if spec.Command == "" {
		return nil, errors.New("ACP agent command is required")
	}
	cmd := exec.CommandContext(ctx, spec.Command, spec.Args...)
	if spec.Dir != "" {
		cmd.Dir = spec.Dir
	}
	cmd.Env = append(os.Environ(), spec.Env...)

	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, fmt.Errorf("ACP stdin: %w", err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, fmt.Errorf("ACP stdout: %w", err)
	}
	if opts.Stderr != nil {
		cmd.Stderr = opts.Stderr
	}

	c := &Client{
		cmd: cmd, stdin: stdin, pending: make(map[string]chan rpcResponse),
		done: make(chan struct{}), opts: opts,
	}
	if c.opts.DecidePermission == nil {
		c.opts.DecidePermission = func(req PermissionRequest) PermissionDecision {
			return ChoosePermission(PermissionDeny, req.Options)
		}
	}
	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("start ACP agent: %w", err)
	}
	go c.readLoop(stdout)
	return c, nil
}

func (c *Client) Initialize(ctx context.Context) (InitializeResult, error) {
	params := map[string]any{
		"protocolVersion":    1,
		"clientCapabilities": map[string]any{},
		"clientInfo":         map[string]any{"name": "devx", "version": "0.1.0"},
	}
	var raw struct {
		ProtocolVersion   int            `json:"protocolVersion"`
		AgentCapabilities map[string]any `json:"agentCapabilities"`
		AgentInfo         struct {
			Name    string `json:"name"`
			Version string `json:"version"`
		} `json:"agentInfo"`
	}
	if err := c.request(ctx, "initialize", params, &raw); err != nil {
		return InitializeResult{}, err
	}
	if raw.ProtocolVersion != 1 {
		return InitializeResult{}, fmt.Errorf("ACP protocol mismatch: agent selected %d, devx supports 1", raw.ProtocolVersion)
	}
	result := InitializeResult{
		ProtocolVersion: raw.ProtocolVersion,
		AgentName:       raw.AgentInfo.Name,
		AgentVersion:    raw.AgentInfo.Version,
		RawCapabilities: raw.AgentCapabilities,
	}
	if v, ok := raw.AgentCapabilities["loadSession"].(bool); ok {
		result.LoadSession = v
	}
	c.init = result
	return result, nil
}

func (c *Client) NewSession(ctx context.Context, cwd string, meta SessionMetadata) (Session, error) {
	abs, err := filepath.Abs(cwd)
	if err != nil {
		return Session{}, fmt.Errorf("resolve cwd: %w", err)
	}
	params := map[string]any{"cwd": abs, "mcpServers": []any{}}
	attachMetadata(params, meta)
	var raw struct {
		SessionID string `json:"sessionId"`
	}
	if err := c.request(ctx, "session/new", params, &raw); err != nil {
		return Session{}, err
	}
	if raw.SessionID == "" {
		return Session{}, errors.New("ACP agent returned empty sessionId")
	}
	return Session{ID: raw.SessionID}, nil
}

func (c *Client) LoadSession(ctx context.Context, sessionID, cwd string, meta SessionMetadata) error {
	if !c.init.LoadSession {
		return errors.New("ACP agent does not advertise loadSession capability")
	}
	abs, err := filepath.Abs(cwd)
	if err != nil {
		return fmt.Errorf("resolve cwd: %w", err)
	}
	params := map[string]any{"sessionId": sessionID, "cwd": abs, "mcpServers": []any{}}
	attachMetadata(params, meta)
	var raw map[string]any
	return c.request(ctx, "session/load", params, &raw)
}

func (c *Client) Prompt(ctx context.Context, sessionID, text string, meta SessionMetadata) (PromptResult, error) {
	params := map[string]any{
		"sessionId": sessionID,
		"prompt":    []map[string]any{{"type": "text", "text": text}},
	}
	attachMetadata(params, meta)
	var raw struct {
		StopReason string `json:"stopReason"`
	}
	if err := c.request(ctx, "session/prompt", params, &raw); err != nil {
		return PromptResult{}, err
	}
	return PromptResult{StopReason: raw.StopReason}, nil
}

func (c *Client) Cancel(sessionID string) error {
	return c.notify("session/cancel", map[string]any{"sessionId": sessionID})
}

func (c *Client) Close() error {
	_ = c.stdin.Close()
	if c.cmd == nil || c.cmd.Process == nil {
		return nil
	}
	select {
	case <-c.done:
		return nil
	default:
	}
	_ = c.cmd.Process.Kill()
	<-c.done
	return nil
}

func ChoosePermission(mode PermissionMode, options []PermissionOption) PermissionDecision {
	preferred := []string{"reject_once", "reject_always"}
	if mode == PermissionAllow {
		preferred = []string{"allow_once", "allow_always"}
	}
	for _, kind := range preferred {
		for _, option := range options {
			if option.Kind == kind {
				return PermissionDecision{OptionID: option.OptionID}
			}
		}
	}
	return PermissionDecision{Cancel: true}
}

func attachMetadata(params map[string]any, meta SessionMetadata) {
	values := map[string]any{}
	if meta.TaskID != "" {
		values["taskId"] = meta.TaskID
	}
	if meta.RunID != "" {
		values["runId"] = meta.RunID
	}
	if len(values) > 0 {
		params["_meta"] = map[string]any{"devPlane": values}
	}
}

func (c *Client) request(ctx context.Context, method string, params any, out any) error {
	id := c.nextID.Add(1)
	key := strconv.FormatInt(id, 10)
	ch := make(chan rpcResponse, 1)
	c.pendingMu.Lock()
	c.pending[key] = ch
	c.pendingMu.Unlock()
	defer func() {
		c.pendingMu.Lock()
		delete(c.pending, key)
		c.pendingMu.Unlock()
	}()

	if err := c.write(map[string]any{"jsonrpc": "2.0", "id": id, "method": method, "params": params}); err != nil {
		return err
	}
	select {
	case response := <-ch:
		if response.Error != nil {
			return fmt.Errorf("ACP %s: (%d) %s", method, response.Error.Code, response.Error.Message)
		}
		if out != nil && len(response.Result) > 0 {
			if err := json.Unmarshal(response.Result, out); err != nil {
				return fmt.Errorf("decode ACP %s result: %w", method, err)
			}
		}
		return nil
	case <-c.done:
		return c.connectionError()
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (c *Client) notify(method string, params any) error {
	return c.write(map[string]any{"jsonrpc": "2.0", "method": method, "params": params})
}

func (c *Client) write(v any) error {
	data, err := json.Marshal(v)
	if err != nil {
		return err
	}
	c.writeMu.Lock()
	defer c.writeMu.Unlock()
	if _, err := c.stdin.Write(append(data, '\n')); err != nil {
		return fmt.Errorf("write ACP message: %w", err)
	}
	return nil
}

func (c *Client) readLoop(r io.Reader) {
	defer close(c.done)
	scanner := bufio.NewScanner(r)
	scanner.Buffer(make([]byte, 64*1024), 16*1024*1024)
	for scanner.Scan() {
		var msg wireMessage
		if err := json.Unmarshal(scanner.Bytes(), &msg); err != nil {
			c.setReadErr(fmt.Errorf("decode ACP message: %w", err))
			return
		}
		if msg.Method != "" {
			if len(msg.ID) > 0 && string(msg.ID) != "null" {
				c.handleAgentRequest(msg)
				continue
			}
			c.handleNotification(msg)
			continue
		}
		key := normalizeID(msg.ID)
		c.pendingMu.Lock()
		ch := c.pending[key]
		c.pendingMu.Unlock()
		if ch != nil {
			ch <- rpcResponse{Result: msg.Result, Error: msg.Error}
		}
	}
	if err := scanner.Err(); err != nil {
		c.setReadErr(fmt.Errorf("read ACP stdout: %w", err))
	}
	_ = c.cmd.Wait()
}

func normalizeID(raw json.RawMessage) string {
	if len(raw) == 0 {
		return ""
	}
	var n int64
	if err := json.Unmarshal(raw, &n); err == nil {
		return strconv.FormatInt(n, 10)
	}
	var s string
	if err := json.Unmarshal(raw, &s); err == nil {
		return s
	}
	return string(raw)
}

func (c *Client) handleNotification(msg wireMessage) {
	if msg.Method != "session/update" || c.opts.OnUpdate == nil {
		return
	}
	var params struct {
		SessionID string         `json:"sessionId"`
		Update    map[string]any `json:"update"`
	}
	if err := json.Unmarshal(msg.Params, &params); err != nil {
		return
	}
	u := Update{SessionID: params.SessionID, Raw: params.Update}
	u.Kind, _ = params.Update["sessionUpdate"].(string)
	if content, ok := params.Update["content"].(map[string]any); ok {
		if content["type"] == "text" {
			u.Text, _ = content["text"].(string)
		}
	}
	if u.Kind == "tool_call" || u.Kind == "tool_call_update" {
		data, _ := json.Marshal(params.Update)
		_ = json.Unmarshal(data, &u.ToolCall)
	}
	c.opts.OnUpdate(u)
}

func (c *Client) handleAgentRequest(msg wireMessage) {
	switch msg.Method {
	case "session/request_permission":
		var req PermissionRequest
		if err := json.Unmarshal(msg.Params, &req); err != nil {
			_ = c.writeError(msg.ID, -32602, "invalid permission request")
			return
		}
		decision := c.opts.DecidePermission(req)
		var outcome map[string]any
		if decision.Cancel || decision.OptionID == "" {
			outcome = map[string]any{"outcome": "cancelled"}
		} else {
			outcome = map[string]any{"outcome": "selected", "optionId": decision.OptionID}
		}
		_ = c.writeRawResponse(msg.ID, map[string]any{"outcome": outcome})
	default:
		_ = c.writeError(msg.ID, -32601, "method not supported by devx")
	}
}

func (c *Client) writeRawResponse(id json.RawMessage, result any) error {
	var idValue any
	if err := json.Unmarshal(id, &idValue); err != nil {
		return err
	}
	return c.write(map[string]any{"jsonrpc": "2.0", "id": idValue, "result": result})
}

func (c *Client) writeError(id json.RawMessage, code int, message string) error {
	var idValue any
	_ = json.Unmarshal(id, &idValue)
	return c.write(map[string]any{"jsonrpc": "2.0", "id": idValue, "error": map[string]any{"code": code, "message": message}})
}

func (c *Client) setReadErr(err error) {
	c.errMu.Lock()
	c.readErr = err
	c.errMu.Unlock()
}
func (c *Client) connectionError() error {
	c.errMu.Lock()
	defer c.errMu.Unlock()
	if c.readErr != nil {
		return c.readErr
	}
	return errors.New("ACP agent exited")
}
