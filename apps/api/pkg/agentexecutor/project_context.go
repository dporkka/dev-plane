package agentexecutor

import (
	"github.com/ai-dev-control-plane/api/internal/agentrunner"
	"github.com/ai-dev-control-plane/projectbrain"
)

// EnableProjectBrain enables the production-safe first-party Project Brain
// source. The lexical repo-intel adapter is revision-checked before emitting
// facts and degrades through compiler warnings when the workspace is not a
// matching local checkout.
func (e *Executor) EnableProjectBrain() *Executor {
	if e == nil || e.runner == nil {
		return e
	}
	e.projectBrainEnabled = true
	e.configureProjectBrain()
	return e
}

// WithGitNexusClient adds GitNexus as an independent Project Brain source while
// retaining the first-party repo-intel fallback. Dev Plane does not construct,
// distribute, or require a GitNexus runtime; callers are responsible for
// providing a client under licensing/deployment terms appropriate to their use.
func (e *Executor) WithGitNexusClient(client projectbrain.GitNexusClient) *Executor {
	if e == nil || e.runner == nil || client == nil {
		return e
	}
	e.projectBrainEnabled = true
	e.gitNexusClient = client
	e.configureProjectBrain()
	return e
}

func (e *Executor) configureProjectBrain() {
	if e == nil || e.runner == nil || !e.projectBrainEnabled {
		return
	}
	e.runner.WithProjectContextProvider(newProjectBrainProvider(e.gitNexusClient))
}

func newProjectBrainProvider(client projectbrain.GitNexusClient) *agentrunner.WorkspaceContextCompiler {
	provider := agentrunner.NewWorkspaceContextCompiler().
		WithSourceFactory(agentrunner.NewRepoIntelContextSourceFactory())
	if client != nil {
		provider.WithSource(projectbrain.NewGitNexusSource(client, projectbrain.GitNexusSourceOptions{}))
	}
	return provider
}
