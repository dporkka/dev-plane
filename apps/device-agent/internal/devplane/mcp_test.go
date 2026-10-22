package devplane

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestMCPInitializeAndReadOnlyTools(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "test.txt"), []byte("hello"), 0600)
	server, err := NewServer(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer server.Close()
	req := []byte(`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-03-26"}}`)
	raw, reply := server.Handle(context.Background(), req)
	if !reply {
		t.Fatal("expected response")
	}
	var msg map[string]any
	if err := json.Unmarshal(raw, &msg); err != nil {
		t.Fatal(err)
	}
	result := msg["result"].(map[string]any)
	if result["protocolVersion"] != "2025-03-26" {
		t.Fatal(result)
	}
	raw, reply = server.Handle(context.Background(), []byte(`{"jsonrpc":"2.0","id":2,"method":"tools/list"}`))
	if !reply || !json.Valid(raw) {
		t.Fatal(string(raw))
	}
	raw, reply = server.Handle(context.Background(), []byte(`{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"files_read","arguments":{"path":"test.txt"}}}`))
	if !reply || !json.Valid(raw) || !contains(string(raw), "hello") {
		t.Fatal(string(raw))
	}
}

func TestMCPRefusesTraversalAndIgnoresNotifications(t *testing.T) {
	server, err := NewServer(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer server.Close()
	raw, reply := server.Handle(context.Background(), []byte(`{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"files_read","arguments":{"path":"../etc/passwd"}}}`))
	if !reply || !contains(string(raw), "isError") {
		t.Fatalf("expected tool error %s", raw)
	}
	_, reply = server.Handle(context.Background(), []byte(`{"jsonrpc":"2.0","method":"notifications/initialized"}`))
	if reply {
		t.Fatal("notification should not get reply")
	}
}

func TestNewServerRequiresExistingDirectory(t *testing.T) {
	if _, err := NewServer(filepath.Join(t.TempDir(), "non-existent")); err == nil {
		t.Fatal("expected error")
	}
}

func contains(s, needle string) bool {
	return len(s) >= len(needle) && (func() bool {
		for i := 0; i <= len(s)-len(needle); i++ {
			if s[i:i+len(needle)] == needle {
				return true
			}
		}
		return false
	})()
}

func TestMCPStdioMultiplexingAndNoNotificationReply(t *testing.T) {
	server, err := NewServer(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer server.Close()
	input := `{"jsonrpc":"2.0","id":1,"method":"ping"}` + "\n" +
		`{"jsonrpc":"2.0","method":"notifications/initialized"}` + "\n" +
		`{"jsonrpc":"2.0","id":2,"method":"tools/list"}` + "\n"
	var output strings.Builder
	if err := server.ServeStdio(context.Background(), strings.NewReader(input), &output); err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSpace(output.String()), "\n")
	if len(lines) != 2 {
		t.Fatalf("expected exactly two replies; got %q", output.String())
	}
	for _, line := range lines {
		if !json.Valid([]byte(line)) {
			t.Fatal(line)
		}
	}
}

func TestMCPSearchAndPaginatedRead(t *testing.T) {
    dir:=t.TempDir()
    os.WriteFile(filepath.Join(dir,"a.txt"),[]byte("needle\nneedle\n"),0600)
    s,err:=NewServer(dir);if err!=nil{t.Fatal(err)};defer s.Close()
    raw,ok:=s.Handle(context.Background(),[]byte("{\"jsonrpc\":\"2.0\",\"id\":1,\"method\":\"tools/call\",\"params\":{\"name\":\"files_search\",\"arguments\":{\"query\":\"needle\",\"pageSize\":1}}}"))
    if !ok || !strings.Contains(string(raw),"nextCursor"){t.Fatalf("search failed %s",raw)}
    raw,ok=s.Handle(context.Background(),[]byte("{\"jsonrpc\":\"2.0\",\"id\":2,\"method\":\"tools/call\",\"params\":{\"name\":\"files_read\",\"arguments\":{\"path\":\"a.txt\",\"maxBytes\":4}}}"))
    if !ok || !strings.Contains(string(raw),"nextOffset") || !strings.Contains(string(raw),"hasMore"){t.Fatalf("paginated file read failed %s",raw)}
}
