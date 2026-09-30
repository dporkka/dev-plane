package codex

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

// StdioConfig controls a local Codex app-server process.
type StdioConfig struct {
	Binary          string
	Args            []string
	Cwd             string
	Env             map[string]string
	ClientName      string
	ClientTitle     string
	ClientVersion   string
	ExperimentalAPI bool
}

// StdioClient is a concurrent newline-delimited JSON-RPC client for
// `codex app-server --listen stdio://`.
type StdioClient struct {
	config StdioConfig

	cmd    *exec.Cmd
	stdin  io.WriteCloser
	stdout io.ReadCloser
	stderr io.ReadCloser

	writeMu sync.Mutex

	nextID    atomic.Int64
	pendingMu sync.Mutex
	pending   map[int64]chan rpcResponse

	subsMu  sync.Mutex
	nextSub uint64
	subs    map[uint64]chan Notification

	requestsMu  sync.Mutex
	nextRequestSub uint64
	requests     map[uint64]chan ServerRequest

	closeOnce sync.Once
	closed    chan struct{}
	readErr   atomic.Value
}

// NewStdioClient starts and initializes Codex app-server.
func NewStdioClient(ctx context.Context, config StdioConfig) (*StdioClient, error) {
	client := &StdioClient{
		config:  normalizeStdioConfig(config),
		pending:  make(map[int64]chan rpcResponse),
		subs:     make(map[uint64]chan Notification),
		requests: make(map[uint64]chan ServerRequest),
		closed:   make(chan struct{}),
	}
	if err := client.start(ctx); err != nil {
		_ = client.Close()
		return nil, err
	}
	return client, nil
}

// NewStdioProvider starts Codex app-server and returns a provider backed by it.
func NewStdioProvider(ctx context.Context, providerConfig Config, stdioConfig StdioConfig) (*Provider, error) {
	client, err := NewStdioClient(ctx, stdioConfig)
	if err != nil {
		return nil, err
	}
	return NewProvider(client, providerConfig), nil
}

func normalizeStdioConfig(config StdioConfig) StdioConfig {
	if strings.TrimSpace(config.Binary) == "" {
		config.Binary = "codex"
	}
	if len(config.Args) == 0 {
		config.Args = []string{"app-server", "--listen", "stdio://"}
	}
	if strings.TrimSpace(config.ClientName) == "" {
		config.ClientName = "dev-plane"
	}
	if strings.TrimSpace(config.ClientTitle) == "" {
		config.ClientTitle = "Dev Plane"
	}
	if strings.TrimSpace(config.ClientVersion) == "" {
		config.ClientVersion = "0.1.0"
	}
	return config
}

func (c *StdioClient) start(ctx context.Context) error {
	c.cmd = exec.Command(c.config.Binary, c.config.Args...)
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
		return fmt.Errorf("codex stdin: %w", err)
	}
	if c.stdout, err = c.cmd.StdoutPipe(); err != nil {
		return fmt.Errorf("codex stdout: %w", err)
	}
	if c.stderr, err = c.cmd.StderrPipe(); err != nil {
		return fmt.Errorf("codex stderr: %w", err)
	}
	if err := c.cmd.Start(); err != nil {
		return fmt.Errorf("start codex app-server: %w", err)
	}

	go c.readLoop()
	go func() {
		_, _ = io.Copy(io.Discard, c.stderr)
	}()

	var initialize map[string]any
	if err := c.Call(ctx, "initialize", map[string]any{
		"clientInfo": map[string]any{
			"name":    c.config.ClientName,
			"title":   c.config.ClientTitle,
			"version": c.config.ClientVersion,
		},
		"capabilities": map[string]any{
			"experimentalApi": c.config.ExperimentalAPI,
		},
	}, &initialize); err != nil {
		return fmt.Errorf("initialize codex app-server: %w", err)
	}
	if err := c.Notify(ctx, "initialized", nil); err != nil {
		return fmt.Errorf("notify codex initialized: %w", err)
	}
	return nil
}

// Call sends one JSON-RPC request and waits for its matching response.
func (c *StdioClient) Call(ctx context.Context, method string, params any, result any) error {
	if c == nil {
		return errors.New("codex stdio client is nil")
	}
	select {
	case <-c.closed:
		return c.closedError()
	default:
	}

	id := c.nextID.Add(1)
	responseCh := make(chan rpcResponse, 1)
	c.pendingMu.Lock()
	c.pending[id] = responseCh
	c.pendingMu.Unlock()

	request := rpcRequest{
		JSONRPC: "2.0",
		ID:      id,
		Method:  method,
		Params:  params,
	}
	if err := c.writeJSON(request); err != nil {
		c.removePending(id)
		return err
	}

	select {
	case <-ctx.Done():
		c.removePending(id)
		return ctx.Err()
	case <-c.closed:
		c.removePending(id)
		return c.closedError()
	case response := <-responseCh:
		if response.Error != nil {
			return response.Error
		}
		if result == nil || len(response.Result) == 0 || string(response.Result) == "null" {
			return nil
		}
		if err := json.Unmarshal(response.Result, result); err != nil {
			return fmt.Errorf("decode codex %s response: %w", method, err)
		}
		return nil
	}
}

// Notify sends a JSON-RPC notification.
func (c *StdioClient) Notify(ctx context.Context, method string, params any) error {
	select {
	case <-ctx.Done():
		return ctx.Err()
	default:
	}
	return c.writeJSON(rpcNotification{JSONRPC: "2.0", Method: method, Params: params})
}

// Subscribe returns an ordered stream of app-server notifications.
func (c *StdioClient) Subscribe() (<-chan Notification, func()) {
	ch := make(chan Notification, 1024)

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

// SubscribeRequests returns client-directed app-server requests that require a response.
// Unsupported request methods remain fail-closed in the transport and are never emitted.
func (c *StdioClient) SubscribeRequests() (<-chan ServerRequest, func()) {
	ch := make(chan ServerRequest, 32)

	c.requestsMu.Lock()
	c.nextRequestSub++
	id := c.nextRequestSub
	select {
	case <-c.closed:
		close(ch)
		c.requestsMu.Unlock()
		return ch, func() {}
	default:
		c.requests[id] = ch
		c.requestsMu.Unlock()
	}

	var once sync.Once
	cancel := func() {
		once.Do(func() {
			c.requestsMu.Lock()
			if existing, ok := c.requests[id]; ok {
				delete(c.requests, id)
				close(existing)
			}
			c.requestsMu.Unlock()
		})
	}
	return ch, cancel
}

// Respond resolves a client-directed JSON-RPC request.
func (c *StdioClient) Respond(ctx context.Context, id json.RawMessage, result any) error {
	select {
	case <-ctx.Done():
		return ctx.Err()
	default:
	}
	if len(id) == 0 {
		return errors.New("codex server request id is required")
	}
	return c.writeJSON(rpcResultResponse{JSONRPC: "2.0", ID: id, Result: result})
}

// Close terminates the app-server process and releases subscribers.
func (c *StdioClient) Close() error {
	if c == nil {
		return nil
	}
	var closeErr error
	c.closeOnce.Do(func() {
		close(c.closed)
		if c.stdin != nil {
			_ = c.stdin.Close()
		}
		if c.cmd != nil && c.cmd.Process != nil {
			if err := c.cmd.Process.Signal(os.Interrupt); err != nil {
				_ = c.cmd.Process.Kill()
			}
			_, _ = c.cmd.Process.Wait()
		}

		c.pendingMu.Lock()
		for id, ch := range c.pending {
			delete(c.pending, id)
			ch <- rpcResponse{Error: &RPCError{Code: -32000, Message: "codex transport closed"}}
			close(ch)
		}
		c.pendingMu.Unlock()

		c.subsMu.Lock()
		for id, ch := range c.subs {
			delete(c.subs, id)
			close(ch)
		}
		c.subsMu.Unlock()

		c.requestsMu.Lock()
		for id, ch := range c.requests {
			delete(c.requests, id)
			close(ch)
		}
		c.requestsMu.Unlock()
	})
	return closeErr
}

func (c *StdioClient) readLoop() {
	scanner := bufio.NewScanner(c.stdout)
	scanner.Buffer(make([]byte, 64*1024), 16*1024*1024)
	for scanner.Scan() {
		line := scanner.Bytes()
		var message wireMessage
		if err := json.Unmarshal(line, &message); err != nil {
			continue
		}

		if len(message.ID) > 0 && message.Method == "" {
			id, err := decodeResponseID(message.ID)
			if err != nil {
				continue
			}
			c.pendingMu.Lock()
			ch, ok := c.pending[id]
			if ok {
				delete(c.pending, id)
			}
			c.pendingMu.Unlock()
			if ok {
				ch <- rpcResponse{Result: message.Result, Error: message.Error}
				close(ch)
			}
			continue
		}

		if message.Method != "" && len(message.ID) == 0 {
			c.broadcast(Notification{Method: message.Method, Params: message.Params})
			continue
		}

		// Surface the approval request methods Dev Plane can safely map. All other
		// client-directed requests fail closed instead of being auto-approved.
		if message.Method != "" && len(message.ID) > 0 {
			if supportsServerRequest(message.Method) {
				c.broadcastRequest(ServerRequest{
					ID:     append(json.RawMessage(nil), message.ID...),
					Method: message.Method,
					Params: append(json.RawMessage(nil), message.Params...),
				})
				continue
			}
			_ = c.writeJSON(rpcErrorResponse{
				JSONRPC: "2.0",
				ID:      message.ID,
				Error: RPCError{
					Code:    -32601,
					Message: "Dev Plane Codex adapter does not handle this client-directed request",
				},
			})
		}
	}
	if err := scanner.Err(); err != nil {
		c.readErr.Store(err)
	}
	_ = c.Close()
}

func (c *StdioClient) broadcast(notification Notification) {
	c.subsMu.Lock()
	defer c.subsMu.Unlock()
	for _, ch := range c.subs {
		select {
		case <-c.closed:
			return
		case ch <- notification:
		}
	}
}

func (c *StdioClient) broadcastRequest(request ServerRequest) {
	c.requestsMu.Lock()
	defer c.requestsMu.Unlock()
	for _, ch := range c.requests {
		select {
		case <-c.closed:
			return
		case ch <- request:
		}
	}
}

func (c *StdioClient) writeJSON(value any) error {
	select {
	case <-c.closed:
		return c.closedError()
	default:
	}
	c.writeMu.Lock()
	defer c.writeMu.Unlock()
	if c.stdin == nil {
		return errors.New("codex stdin is unavailable")
	}
	encoder := json.NewEncoder(c.stdin)
	if err := encoder.Encode(value); err != nil {
		return fmt.Errorf("write codex json-rpc message: %w", err)
	}
	return nil
}

func (c *StdioClient) removePending(id int64) {
	c.pendingMu.Lock()
	delete(c.pending, id)
	c.pendingMu.Unlock()
}

func (c *StdioClient) closedError() error {
	if value := c.readErr.Load(); value != nil {
		if err, ok := value.(error); ok {
			return fmt.Errorf("codex transport closed: %w", err)
		}
	}
	return errors.New("codex transport closed")
}

func decodeResponseID(raw json.RawMessage) (int64, error) {
	var id int64
	if err := json.Unmarshal(raw, &id); err != nil {
		return 0, fmt.Errorf("decode codex response id: %w", err)
	}
	return id, nil
}

// RPCError is a JSON-RPC error returned by Codex.
type RPCError struct {
	Code    int             `json:"code"`
	Message string          `json:"message"`
	Data    json.RawMessage `json:"data,omitempty"`
}

func (e *RPCError) Error() string {
	if e == nil {
		return ""
	}
	return fmt.Sprintf("codex rpc error %d: %s", e.Code, e.Message)
}

type rpcRequest struct {
	JSONRPC string `json:"jsonrpc"`
	ID      int64  `json:"id"`
	Method  string `json:"method"`
	Params  any    `json:"params,omitempty"`
}

type rpcNotification struct {
	JSONRPC string `json:"jsonrpc"`
	Method  string `json:"method"`
	Params  any    `json:"params,omitempty"`
}

type rpcErrorResponse struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id"`
	Error   RPCError        `json:"error"`
}

type rpcResultResponse struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id"`
	Result  any             `json:"result"`
}

type wireMessage struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Method  string          `json:"method,omitempty"`
	Params  json.RawMessage `json:"params,omitempty"`
	Result  json.RawMessage `json:"result,omitempty"`
	Error   *RPCError       `json:"error,omitempty"`
}

type rpcResponse struct {
	Result json.RawMessage
	Error  *RPCError
}
