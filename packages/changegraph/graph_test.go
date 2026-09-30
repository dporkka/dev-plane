package changegraph

import (
	"reflect"
	"strings"
	"testing"
)

func TestValidateAcceptsDependencyDAG(t *testing.T) {
	graph := Graph{Nodes: []Node{
		{ID: "schema"},
		{ID: "api", DependsOn: []string{"schema"}},
		{ID: "sdk", DependsOn: []string{"api"}},
		{ID: "web", DependsOn: []string{"sdk"}},
	}}
	if err := Validate(graph); err != nil {
		t.Fatalf("Validate() error: %v", err)
	}
}

func TestValidateRejectsCycle(t *testing.T) {
	graph := Graph{Nodes: []Node{
		{ID: "api", DependsOn: []string{"web"}},
		{ID: "web", DependsOn: []string{"api"}},
	}}
	err := Validate(graph)
	if err == nil || !strings.Contains(err.Error(), "cycle") {
		t.Fatalf("Validate() error = %v, want cycle error", err)
	}
}

func TestValidateRejectsUnknownAndDuplicateDependencies(t *testing.T) {
	tests := []struct {
		name  string
		graph Graph
		want  string
	}{
		{
			name: "unknown",
			graph: Graph{Nodes: []Node{{ID: "api", DependsOn: []string{"schema"}}}},
			want: "unknown candidate",
		},
		{
			name: "duplicate edge",
			graph: Graph{Nodes: []Node{
				{ID: "schema"},
				{ID: "api", DependsOn: []string{"schema", "schema"}},
			}},
			want: "duplicate dependency",
		},
		{
			name: "self edge",
			graph: Graph{Nodes: []Node{{ID: "api", DependsOn: []string{"api"}}}},
			want: "cycle",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := Validate(tt.graph)
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("Validate() error = %v, want %q", err, tt.want)
			}
		})
	}
}

func TestBlockersReturnsUnmergedTransitiveDependencies(t *testing.T) {
	graph := Graph{Nodes: []Node{
		{ID: "schema"},
		{ID: "api", DependsOn: []string{"schema"}},
		{ID: "sdk", DependsOn: []string{"api"}},
		{ID: "web", DependsOn: []string{"sdk"}},
	}}
	states := map[string]State{
		"schema": StateMerged,
		"api":    StateOpen,
		"sdk":    StateOpen,
		"web":    StateOpen,
	}

	got, err := Blockers(graph, "web", states)
	if err != nil {
		t.Fatalf("Blockers() error: %v", err)
	}
	want := []string{"api", "sdk"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("Blockers() = %#v, want %#v", got, want)
	}
}

func TestBlockersIsEmptyWhenAllAncestorsMerged(t *testing.T) {
	graph := Graph{Nodes: []Node{
		{ID: "schema"},
		{ID: "api", DependsOn: []string{"schema"}},
	}}
	got, err := Blockers(graph, "api", map[string]State{
		"schema": StateMerged,
		"api": StateOpen,
	})
	if err != nil {
		t.Fatalf("Blockers() error: %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("Blockers() = %#v, want empty", got)
	}
}
