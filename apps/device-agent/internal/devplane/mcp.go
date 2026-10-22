package devplane

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
)

const ProtocolVersion = "2025-03-26"

type Server struct {
	workspace *Workspace
	runner    *JobRunner
	search    *SearchManager
}

func NewServer(root string) (*Server, error) {
	ws, err := NewWorkspace(root)
	if err != nil {
		return nil, err
	}
	search, err := NewSearchManager(ws)
	if err != nil { ws.Close(); return nil, err }
	return &Server{workspace: ws, runner: NewJobRunner(2), search: search}, nil
}
func (s *Server) Close() error { s.runner.Shutdown(); return s.workspace.Close() }

type rpcRequest struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params"`
}

type rpcFailure struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

func response(id json.RawMessage, result any, failure *rpcFailure) []byte {
	msg := map[string]any{"jsonrpc": "2.0", "id": id}
	if failure != nil {
		msg["error"] = failure
	} else {
		msg["result"] = result
	}
	data, _ := json.Marshal(msg)
	return data
}

func (s *Server) Handle(ctx context.Context, line []byte) ([]byte, bool) {
	var req rpcRequest
	if err := json.Unmarshal(line, &req); err != nil {
		return response(json.RawMessage("null"), nil, &rpcFailure{-32700, "parse error"}), true
	}
	if req.JSONRPC != "2.0" || req.Method == "" {
		return response(json.RawMessage("null"), nil, &rpcFailure{-32600, "invalid request"}), true
	}
	if len(req.ID) == 0 || strings.HasPrefix(req.Method, "notifications/") {
		return nil, false
	}
	switch req.Method {
	case "initialize":
		var p struct {
			ProtocolVersion string `json:"protocolVersion"`
		}
		_ = json.Unmarshal(req.Params, &p)
		v := ProtocolVersion
		if p.ProtocolVersion == "2024-11-05" || p.ProtocolVersion == "2025-03-26" || p.ProtocolVersion == "2025-06-18" {
			v = p.ProtocolVersion
		}
		return response(req.ID, map[string]any{"protocolVersion": v, "capabilities": map[string]any{"tools": map[string]any{}}, "serverInfo": map[string]any{"name": "devplane-local", "version": "0.1.0"}, "instructions": "Read-only local development tools. No remote listener or arbitrary shell commands."}, nil), true
	case "ping":
		return response(req.ID, map[string]any{}, nil), true
	case "tools/list":
		return response(req.ID, map[string]any{"tools": toolCatalog()}, nil), true
	case "tools/call":
		var p struct {
			Name      string          `json:"name"`
			Arguments json.RawMessage `json:"arguments"`
		}
		if err := json.Unmarshal(req.Params, &p); err != nil || p.Name == "" {
			return response(req.ID, nil, &rpcFailure{-32602, "invalid tool arguments"}), true
		}
		result, err := s.callTool(ctx, p.Name, p.Arguments)
		if err != nil {
			return response(req.ID, map[string]any{"content": []any{map[string]any{"type": "text", "text": err.Error()}}, "isError": true}, nil), true
		}
		encoded, _ := json.Marshal(result)
		return response(req.ID, map[string]any{"content": []any{map[string]any{"type": "text", "text": string(encoded)}}}, nil), true
	default:
		return response(req.ID, nil, &rpcFailure{-32601, "method not found"}), true
	}
}

func paramSchema(props map[string]any, required ...string) map[string]any {
	if required == nil {
		required = []string{}
	}
	return map[string]any{"type": "object", "properties": props, "required": required, "additionalProperties": false}
}
func strSchema(desc string) map[string]any {
	return map[string]any{"type": "string", "description": desc}
}
func intSchema(desc string) map[string]any {
	return map[string]any{"type": "integer", "description": desc}
}
func tool(name, desc string, schema map[string]any) map[string]any {
	return map[string]any{"name": name, "description": desc, "inputSchema": schema}
}

func toolCatalog() []any {
	return []any{
		tool("workspaces_list", "Show the single explicitly authorized workspace", paramSchema(map[string]any{})),
		tool("files_list", "List one directory without following symlinks, limited to authorized workspace", paramSchema(map[string]any{"path": strSchema("Workspace-relative directory; default .")})),
		tool("files_read", "Read text from regular file in authorized workspace (max 65536 bytes)", paramSchema(map[string]any{"path": strSchema("Workspace-relative file path"), "offset": intSchema("Byte offset, >= 0"), "maxBytes": intSchema("Byte count, max 65536")}, "path")),
		tool("files_search", "Bounded local literal search, no symlinks, with paginated results", paramSchema(map[string]any{"query": strSchema("Literal case-insensitive query, 1-200 bytes"), "directory": strSchema("Relative directory, default ."), "pageSize": intSchema("Page size 1-50; default 20"), "cursor": strSchema("Opaque cursor from previous result; omit query on continuation")})),
		tool("jobs_start", "Start a bounded, read-only Git inspection job; no shell execution", paramSchema(map[string]any{"profile": map[string]any{"type": "string", "enum": []string{"git-status", "git-diff", "git-log"}}}, "profile")),
		tool("jobs_status", "Read a job's status and bounded output", paramSchema(map[string]any{"id": strSchema("Job ID")}, "id")),
		tool("jobs_cancel", "Cancel a running job", paramSchema(map[string]any{"id": strSchema("Job ID")}, "id")),
	}
}

func (s *Server) callTool(ctx context.Context, name string, raw json.RawMessage) (any, error) {
	if len(raw) == 0 {
		raw = []byte("{}")
	}
	var args map[string]json.RawMessage
	if err := json.Unmarshal(raw, &args); err != nil {
		return nil, errors.New("arguments must be a JSON object")
	}
	readStr := func(key string, required bool) (string, error) {
		v, ok := args[key]
		if !ok {
			if required {
				return "", fmt.Errorf("missing %s", key)
			}
			return "", nil
		}
		var result string
		if err := json.Unmarshal(v, &result); err != nil {
			return "", fmt.Errorf("%s must be a string", key)
		}
		if required && result == "" {
			return "", fmt.Errorf("%s cannot be empty", key)
		}
		return result, nil
	}
	switch name {
	case "workspaces_list":
		return []map[string]string{{"name": "default", "root": s.workspace.Root, "mode": "read-only"}}, nil
	case "files_list":
		path, err := readStr("path", false)
		if err != nil {
			return nil, err
		}
		if path == "" {
			path = "."
		}
		return s.workspace.ListDir(path)
	case "files_read":
		path, err := readStr("path", true)
		if err != nil {
			return nil, err
		}
		offset := int64(0)
		maxBytes := MaxReadBytes
		if v, ok := args["offset"]; ok {
			if err := json.Unmarshal(v, &offset); err != nil {
				return nil, errors.New("invalid offset")
			}
		}
		if v, ok := args["maxBytes"]; ok {
			if err := json.Unmarshal(v, &maxBytes); err != nil {
				return nil, errors.New("invalid maxBytes")
			}
		}
        if maxBytes == 0 { maxBytes = MaxReadBytes }
        return s.workspace.ReadPage(path, offset, maxBytes)
    case "files_search":
        pageSize := 20
        if v, ok := args["pageSize"]; ok {
            if err := json.Unmarshal(v, &pageSize); err != nil { return nil, errors.New("invalid pageSize") }
        }
        cursor, err := readStr("cursor", false)
        if err != nil { return nil, err }
        if cursor != "" {
            if _, ok := args["query"]; ok { return nil, errors.New("query cannot be supplied with cursor") }
            if _, ok := args["directory"]; ok { return nil, errors.New("directory cannot be supplied with cursor") }
            return s.search.Continue(cursor, pageSize)
        }
        query, err := readStr("query", true)
        if err != nil { return nil, err }
        directory, err := readStr("directory", false)
        if err != nil { return nil, err }
        return s.search.Start(ctx, query, directory, pageSize)
    case "jobs_start":
		profile, err := readStr("profile", true)
		if err != nil {
			return nil, err
		}
		return s.runner.Start(context.Background(), s.workspace.Root, profile)
	case "jobs_status":
		id, err := readStr("id", true)
		if err != nil {
			return nil, err
		}
		return s.runner.Status(id)
	case "jobs_cancel":
		id, err := readStr("id", true)
		if err != nil {
			return nil, err
		}
		if err := s.runner.Cancel(id); err != nil {
			return nil, err
		}
		return map[string]string{"id": id, "request": "cancellation requested"}, nil
	default:
		return nil, errors.New("unknown tool: " + name)
	}
}

// ServeStdio speaks newline-delimited JSON-RPC. No TCP/HTTP socket is opened.
func (s *Server) ServeStdio(ctx context.Context, in io.Reader, out io.Writer) error {
	scan := bufio.NewScanner(in)
	scan.Buffer(make([]byte, 4096), 8*1024*1024)
	writer := bufio.NewWriter(out)
	defer writer.Flush()
	for scan.Scan() {
		raw, reply := s.Handle(ctx, scan.Bytes())
		if !reply {
			continue
		}
		if _, err := writer.Write(raw); err != nil {
			return err
		}
		if err := writer.WriteByte('\n'); err != nil {
			return err
		}
		if err := writer.Flush(); err != nil {
			return err
		}
	}
	if err := scan.Err(); err != nil {
		return fmt.Errorf("stdin JSON-RPC: %w", err)
	}
	return nil
}
