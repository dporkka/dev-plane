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
