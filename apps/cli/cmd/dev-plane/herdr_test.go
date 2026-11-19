package main

import (
	"encoding/json"
	"strings"
	"testing"
)

const herdrFixture = `{
  "id":"cli:api:snapshot",
  "result":{"type":"session_snapshot","snapshot":{
    "version":"0.8.0","protocol":19,
    "agents":[
      {"agent":"codex","agent_status":"working","pane_id":"pane-b","workspace_id":"ws-2","focused":false,"cwd":"/private/customer/project","terminal_title":"SECRET_ACCESS_TOKEN=should_not_leak","agent_session":{"value":"/private/session.json"}},
      {"agent":"omp","agent_status":"idle","pane_id":"pane-a","workspace_id":"ws-1","focused":true,"cwd":"/private/finance"}
    ]
  }}
}`

func TestParseHerdrSnapshotProjectsOnlySafeFieldsAndSorts(t *testing.T) {
	got, err := parseHerdrSnapshot([]byte(herdrFixture))
	if err != nil {
		t.Fatal(err)
	}
	if got.Source != "herdr" || got.Version != "0.8.0" || got.Protocol != 19 || got.Total != 2 {
		t.Fatalf("unexpected metadata: %+v", got)
	}
	if got.Agents[0].PaneID != "pane-a" || got.Agents[0].Status != "idle" || !got.Agents[0].Focused {
		t.Fatalf("not sorted or invalid: %+v", got.Agents)
	}
	b, err := json.Marshal(got)
	if err != nil {
		t.Fatal(err)
	}
	for _, forbidden := range []string{"/private", "SECRET_ACCESS_TOKEN", "terminal_title", "agent_session", "cwd", "should_not_leak"} {
		if strings.Contains(string(b), forbidden) {
			t.Fatalf("leaked sensitive field %q in %s", forbidden, b)
		}
	}
}

func TestParseHerdrSnapshotAllowsNoAgents(t *testing.T) {
	input := `{"result":{"type":"session_snapshot","snapshot":{"version":"0.8.0","protocol":19,"agents":[]}}}`
	got, err := parseHerdrSnapshot([]byte(input))
	if err != nil {
		t.Fatal(err)
	}
	if got.Total != 0 || got.Agents == nil {
		t.Fatalf("empty list should be valid JSON array: %+v", got)
	}
}

func TestParseHerdrSnapshotFailsClosedOnMalformedProviderPayload(t *testing.T) {
	cases := []string{
		`not json`,
		`{}`,
		`{"result":{"type":"error","snapshot":{"version":"0.8.0","protocol":19,"agents":[]}}}`,
		`{"result":{"type":"session_snapshot","snapshot":{"version":"0.8.0","protocol":19}}}`,
		`{"result":{"type":"session_snapshot","snapshot":{"version":"0.8.0","protocol":19,"agents":[{"agent":"codex","agent_status":"idle","pane_id":"x","workspace_id":"w"},{"agent":"claude","agent_status":"working","pane_id":"x","workspace_id":"w"}]}}}`,
		`{"result":{"type":"session_snapshot","snapshot":{"version":"0.8.0","protocol":19,"agents":[{"agent":"codex","agent_status":"done\\nINJECT","pane_id":"x","workspace_id":"w"}]}}}`,
		`{"result":{"type":"session_snapshot","snapshot":{"version":"0.8.0","protocol":19,"agents":[{"agent":"codex","agent_status":"idle","pane_id":"","workspace_id":"w"}]}}}`,
	}
	for i, input := range cases {
		if _, err := parseHerdrSnapshot([]byte(input)); err == nil {
			t.Errorf("case %d unexpectedly accepted", i)
		}
	}
}

func TestAgentsLocalCLIRejectsUnsupportedArguments(t *testing.T) {
	if err := run([]string{"agents", "local", "--command", "rm"}); err == nil {
		t.Fatal("unrecognized flags must be rejected rather than passed to Herdr")
	}
	if err := run([]string{"agents", "remote"}); err == nil {
		t.Fatal("unsupported remote control must be rejected")
	}
}
func TestHerdrSnapshotCaptureStopsAtFourMiB(t *testing.T) {
	cancels := 0
	writer := &herdrSnapshotCapture{cancel: func() { cancels++ }}
	initial := []byte(strings.Repeat("x", herdrMaxSnapshotBytes))
	n, err := writer.Write(initial)
	if err != nil || n != len(initial) || writer.overLimit || cancels != 0 {
		t.Fatalf("exact limit should succeed: n=%d err=%v exceeded=%t cancels=%d", n, err, writer.overLimit, cancels)
	}
	n, err = writer.Write([]byte("y"))
	if err != nil || n != 1 || !writer.overLimit || cancels != 1 || writer.buf.Len() != herdrMaxSnapshotBytes+1 {
		t.Fatalf("one byte over limit must cancel: n=%d err=%v len=%d cancels=%d", n, err, writer.buf.Len(), cancels)
	}
	n, err = writer.Write([]byte(strings.Repeat("z", 10000)))
	if err != nil || n != 10000 || cancels != 1 || writer.buf.Len() != herdrMaxSnapshotBytes+1 {
		t.Fatal("subsequent writes must remain bounded and cancel once")
	}
}

func TestHerdrSnapshotCaptureLargeSingleWriteIsBounded(t *testing.T) {
	cancels := 0
	writer := &herdrSnapshotCapture{cancel: func() { cancels++ }}
	oversized := []byte(strings.Repeat("x", herdrMaxSnapshotBytes+5000))
	n, err := writer.Write(oversized)
	if err != nil || n != len(oversized) || !writer.overLimit || cancels != 1 ||
		writer.buf.Len() != herdrMaxSnapshotBytes+1 {
		t.Fatalf("oversized write: n=%d err=%v retained=%d", n, err, writer.buf.Len())
	}
}
