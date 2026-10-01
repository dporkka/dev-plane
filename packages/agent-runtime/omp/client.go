package omp

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
	"sync"
	"sync/atomic"
)

// SessionBinding is the durable OMP session identity returned by open_session.
type SessionBinding struct {
	SessionID   string
	SessionFile string
	Resumed     bool
}

// PromptError is the provider failure payload carried by prompt_result.
type PromptError struct {
	Message   string `json:"message"`
	Provider  string `json:"provider,omitempty"`
	Model     string `json:"model,omitempty"`
	HTTPStatus int   `json:"httpStatus,omitempty"`
	Retryable bool   `json:"retryable"`
}

// Frame is the subset of OMP RPC frames Dev Plane currently normalizes.
// Raw preserves the complete provider payload for durable evidence/debugging.
type Frame struct {
	Type           string
	ID             string
	MessageID      string
	ToolCallID     string
	ToolName       string
	Status         string
	SessionSettled bool
	IsError        bool
	PromptError    *PromptError
	Raw            json.RawMessage
}

// RPCClient is the OMP transport surface used by Provider.
// StdioClient is the production implementation; tests can supply an in-memory fake.
type RPCClient interface {
	OpenSession(ctx context.Context, sessionDir string) (SessionBinding, error)
	SetModel(ctx context.Context, provider, modelID string) error
	Prompt(ctx context.Context, message string) (string, error)
	Steer(ctx context.Context, message string) error
	Abort(ctx context.Context) error
	Compact(ctx context.Context) error
	Subscribe() (<-chan Frame, func())
	Close() error
}

// ClientFactory constructs one isolated OMP RPC process for one durable thread.
type ClientFactory func(ctx context.Context, config StdioConfig) (RPCClient, error)

// StdioConfig controls a local `omp --mode rpc --no-ui` process.
type StdioConfig struct {
	Binary             string
	Args               []string
	Cwd                string
	Env                map[string]string
	AppendSystemPrompt string
}

type rpcResponse struct {
	ID      string          `json:"id"`
	Type    string          `json:"type"`
	Command string          `json:"command"`
	Success bool            `json:"success"`
	Data    json.RawMessage `json:"data"`
	Error   string          `json:"error"`
	Code    string          `json:"code,omitempty"`
}

type readyFrame struct {
	Type                     string `json:"type"`
	ProtocolVersion          int    `json:"protocolVersion"`
	MaxFrameBytes            int    `json:"maxFrameBytes"`
	MaxReassembledFrameBytes int    `json:"maxReassembledFrameBytes"`
}

// StdioClient implements the documented newline-delimited OMP RPC protocol.
// This first adapter intentionally stays on protocol v1; the runtime only
// advertises capabilities whose command/event semantics are handled here.
type StdioClient struct {
	config StdioConfig

	cmd    *exec.Cmd
	stdin  io.WriteCloser
	stdout io.ReadCloser
	stderr io.ReadCloser
	reader *bufio.Reader

	writeMu sync.Mutex
	nextID  atomic.Uint64

	pendingMu sync.Mutex
	pending   map[string]chan rpcResponse

	subsMu  sync.Mutex
	nextSub uint64
	subs    map[uint64]chan Frame

	closeOnce sync.Once
	closed    chan struct{}
	errMu     sync.Mutex
	readErr   error
}

// NewStdioClient starts OMP RPC and waits for its ready frame.
func NewStdioClient(ctx context.Context, config StdioConfig) (*StdioClient, error) {
	client := &StdioClient{
		config:  normalizeStdioConfig(config),
		pending: make(map[string]chan rpcResponse),
		subs:    make(map[uint64]chan Frame),
		closed:  make(chan struct{}),
	}
	if err := client.start(ctx); err != nil {
		_ = client.Close()
		return nil, err
	}
	return client, nil
}

func normalizeStdioConfig(config StdioConfig) StdioConfig {
	if strings.TrimSpace(config.Binary) == "" {
		config.Binary = "omp"
	}
	if len(config.Args) == 0 {
		config.Args = []string{"--mode", "rpc", "--no-ui"}
	}
	return config
}

func (c *StdioClient) start(ctx context.Context) error {
	args := append([]string(nil), c.config.Args...)
	if strings.TrimSpace(c.config.AppendSystemPrompt) != "" {
		args = append(args, "--append-system-prompt", c.config.AppendSystemPrompt)
	}
	c.cmd = exec.Command(c.config.Binary, args...)
	c.cmd.Dir = c.config.Cwd
	if len(c.config.Env) > 0 {
		env := os.Environ()
		for key, value := range c.config.Env {
			env = append(env, key+"="+value)
		}
		c.cmd.Env = env
	}

	var err error
	if c.stdin, err = c.cmd.StdinPipe(); err != nil {
		return fmt.Errorf("omp stdin: %w", err)
	}
	if c.stdout, err = c.cmd.StdoutPipe(); err != nil {
		return fmt.Errorf("omp stdout: %w", err)
	}
	if c.stderr, err = c.cmd.StderrPipe(); err != nil {
		return fmt.Errorf("omp stderr: %w", err)
	}
	if err := c.cmd.Start(); err != nil {
		return fmt.Errorf("start omp rpc: %w", err)
	}

	go func() { _, _ = io.Copy(io.Discard, c.stderr) }()
	c.reader = bufio.NewReader(c.stdout)

	type readyResult struct {
		frame readyFrame
		err   error
	}
	readyCh := make(chan readyResult, 1)
	go func() {
		raw, err := c.reader.ReadBytes('\n')
		if err != nil {
			readyCh <- readyResult{err: err}
			return
		}
		var ready readyFrame
		if err := json.Unmarshal(raw, &ready); err != nil {
			readyCh <- readyResult{err: fmt.Errorf("decode omp ready frame: %w", err)}
			return
		}
		readyCh <- readyResult{frame: ready}
	}()

	select {
	case <-ctx.Done():
		if c.cmd.Process != nil {
			_ = c.cmd.Process.Kill()
		}
		return ctx.Err()
	case result := <-readyCh:
		if result.err != nil {
			return fmt.Errorf("read omp ready frame: %w", result.err)
		}
		if result.frame.Type != "ready" {
			return fmt.Errorf("omp rpc expected ready frame, got %q", result.frame.Type)
		}
	}

	go c.readLoop()
	return nil
}

func (c *StdioClient) OpenSession(ctx context.Context, sessionDir string) (SessionBinding, error) {
	var data struct {
		Cancelled   bool   `json:"cancelled"`
		Resumed     bool   `json:"resumed"`
		SessionID   string `json:"sessionId"`
		SessionFile string `json:"sessionFile"`
	}
	_, err := c.call(ctx, map[string]any{"type": "open_session", "sessionDir": sessionDir}, &data)
	if err != nil {
		return SessionBinding{}, fmt.Errorf("omp open_session: %w", err)
	}
	if data.Cancelled {
		return SessionBinding{}, errors.New("omp open_session was cancelled")
	}
	if strings.TrimSpace(data.SessionID) == "" {
		return SessionBinding{}, errors.New("omp open_session returned empty session id")
	}
	return SessionBinding{SessionID: data.SessionID, SessionFile: data.SessionFile, Resumed: data.Resumed}, nil
}

func (c *StdioClient) SetModel(ctx context.Context, provider, modelID string) error {
	if strings.TrimSpace(provider) == "" || strings.TrimSpace(modelID) == "" {
		return errors.New("omp model provider and model id are required")
	}
	_, err := c.call(ctx, map[string]any{"type": "set_model", "provider": provider, "modelId": modelID}, nil)
	if err != nil {
		return fmt.Errorf("omp set_model: %w", err)
	}
	return nil
}

func (c *StdioClient) Prompt(ctx context.Context, message string) (string, error) {
	id, err := c.call(ctx, map[string]any{"type": "prompt", "message": message}, nil)
	if err != nil {
		return "", fmt.Errorf("omp prompt: %w", err)
	}
	return id, nil
}

func (c *StdioClient) Steer(ctx context.Context, message string) error {
	_, err := c.call(ctx, map[string]any{"type": "steer", "message": message}, nil)
	if err != nil {
		return fmt.Errorf("omp steer: %w", err)
	}
	return nil
}

func (c *StdioClient) Abort(ctx context.Context) error {
	_, err := c.call(ctx, map[string]any{"type": "abort"}, nil)
	if err != nil {
		return fmt.Errorf("omp abort: %w", err)
	}
	return nil
}

func (c *StdioClient) Compact(ctx context.Context) error {
	_, err := c.call(ctx, map[string]any{"type": "compact"}, nil)
	if err != nil {
		return fmt.Errorf("omp compact: %w", err)
	}
	return nil
}

func (c *StdioClient) call(ctx context.Context, command map[string]any, result any) (string, error) {
	if c == nil {
		return "", errors.New("omp stdio client is nil")
	}
	select {
	case <-c.closed:
		return "", c.closedError()
	default:
	}

	id := fmt.Sprintf("req_%d", c.nextID.Add(1))
	request := make(map[string]any, len(command)+1)
	for key, value := range command {
		request[key] = value
	}
	request["id"] = id

	responseCh := make(chan rpcResponse, 1)
	c.pendingMu.Lock()
	c.pending[id] = responseCh
	c.pendingMu.Unlock()

	if err := c.writeJSON(request); err != nil {
		c.removePending(id)
		return "", err
	}

	select {
	case <-ctx.Done():
		c.removePending(id)
		return "", ctx.Err()
	case <-c.closed:
		c.removePending(id)
		return "", c.closedError()
	case response := <-responseCh:
		if !response.Success {
			if strings.TrimSpace(response.Code) != "" {
				return "", fmt.Errorf("%s (%s)", response.Error, response.Code)
			}
			return "", errors.New(response.Error)
		}
		if result != nil && len(response.Data) > 0 && string(response.Data) != "null" {
			if err := json.Unmarshal(response.Data, result); err != nil {
				return "", fmt.Errorf("decode omp %s response: %w", response.Command, err)
			}
		}
		return id, nil
	}
}

func (c *StdioClient) Subscribe() (<-chan Frame, func()) {
	ch := make(chan Frame, 256)
	c.subsMu.Lock()
	c.nextSub++
	id := c.nextSub
	select {
	case <-c.closed:
		close(ch)
		c.subsMu.Unlock()
		return ch, func() {}
	default:
		c.subs[id] = ch
		c.subsMu.Unlock()
	}

	var once sync.Once
	cancel := func() {
		once.Do(func() {
			c.subsMu.Lock()
			if existing, ok := c.subs[id]; ok {
				delete(c.subs, id)
				close(existing)
			}
			c.subsMu.Unlock()
		})
	}
	return ch, cancel
}

func (c *StdioClient) readLoop() {
	for {
		raw, err := c.reader.ReadBytes('\n')
		if err != nil {
			if !errors.Is(err, io.EOF) {
				c.closeWithError(fmt.Errorf("read omp rpc: %w", err))
			} else {
				c.closeWithError(io.EOF)
			}
			return
		}
		trimmed := strings.TrimSpace(string(raw))
		if trimmed == "" {
			continue
		}
		payload := json.RawMessage(append([]byte(nil), raw...))
		var envelope struct {
			Type string `json:"type"`
			ID   string `json:"id,omitempty"`
		}
		if err := json.Unmarshal(payload, &envelope); err != nil {
			c.closeWithError(fmt.Errorf("decode omp frame: %w", err))
			return
		}
		if envelope.Type == "response" {
			var response rpcResponse
			if err := json.Unmarshal(payload, &response); err != nil {
				c.closeWithError(fmt.Errorf("decode omp response: %w", err))
				return
			}
			c.deliverResponse(response)
			continue
		}
		frame, err := decodeFrame(payload)
		if err != nil {
			c.closeWithError(err)
			return
		}
		c.broadcast(frame)
	}
}

func decodeFrame(raw json.RawMessage) (Frame, error) {
	var wire struct {
		Type           string       `json:"type"`
		ID             string       `json:"id,omitempty"`
		MessageID      string       `json:"messageId,omitempty"`
		ToolCallID     string       `json:"toolCallId,omitempty"`
		ToolName       string       `json:"toolName,omitempty"`
		Status         string       `json:"status,omitempty"`
		SessionSettled bool         `json:"sessionSettled,omitempty"`
		IsError        bool         `json:"isError,omitempty"`
		Error          *PromptError `json:"error,omitempty"`
	}
	if err := json.Unmarshal(raw, &wire); err != nil {
		return Frame{}, fmt.Errorf("decode omp event: %w", err)
	}
	return Frame{
		Type:           wire.Type,
		ID:             wire.ID,
		MessageID:      wire.MessageID,
		ToolCallID:     wire.ToolCallID,
		ToolName:       wire.ToolName,
		Status:         wire.Status,
		SessionSettled: wire.SessionSettled,
		IsError:        wire.IsError,
		PromptError:    wire.Error,
		Raw:            append(json.RawMessage(nil), raw...),
	}, nil
}

func (c *StdioClient) deliverResponse(response rpcResponse) {
	c.pendingMu.Lock()
	ch, ok := c.pending[response.ID]
	if ok {
		delete(c.pending, response.ID)
	}
	c.pendingMu.Unlock()
	if ok {
		ch <- response
	}
}

func (c *StdioClient) removePending(id string) {
	c.pendingMu.Lock()
	delete(c.pending, id)
	c.pendingMu.Unlock()
}

func (c *StdioClient) broadcast(frame Frame) {
	c.subsMu.Lock()
	defer c.subsMu.Unlock()
	for _, ch := range c.subs {
		select {
		case <-c.closed:
			return
		case ch <- frame:
		}
	}
}

func (c *StdioClient) writeJSON(value any) error {
	payload, err := json.Marshal(value)
	if err != nil {
		return fmt.Errorf("encode omp rpc request: %w", err)
	}
	payload = append(payload, '\n')
	c.writeMu.Lock()
	defer c.writeMu.Unlock()
	select {
	case <-c.closed:
		return c.closedError()
	default:
	}
	if _, err := c.stdin.Write(payload); err != nil {
		return fmt.Errorf("write omp rpc request: %w", err)
	}
	return nil
}

func (c *StdioClient) Close() error {
	if c == nil {
		return nil
	}
	if c.stdin != nil {
		_ = c.stdin.Close()
	}
	if c.cmd != nil && c.cmd.Process != nil {
		_ = c.cmd.Process.Kill()
	}
	c.closeWithError(errors.New("omp stdio client closed"))
	return nil
}

func (c *StdioClient) closeWithError(err error) {
	c.closeOnce.Do(func() {
		c.errMu.Lock()
		c.readErr = err
		c.errMu.Unlock()
		close(c.closed)

		c.subsMu.Lock()
		for id, ch := range c.subs {
			delete(c.subs, id)
			close(ch)
		}
		c.subsMu.Unlock()
	})
}

func (c *StdioClient) closedError() error {
	c.errMu.Lock()
	defer c.errMu.Unlock()
	if c.readErr != nil {
		return c.readErr
	}
	return errors.New("omp stdio client closed")
}
