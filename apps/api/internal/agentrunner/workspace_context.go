package agentrunner

import (
	"context"

	"github.com/ai-dev-control-plane/projectbrain"
)

// WorkspaceProjectContextProvider is an optional extension implemented by
// providers that need the concrete workspace path to build revision-bound
// context. prepareProjectContext prefers this method when available.
type WorkspaceProjectContextProvider interface {
	CompileWorkspace(context.Context, projectbrain.CompileRequest, string) (projectbrain.ContextPackage, error)
}

// WorkspaceContextSourceFactory creates one Project Brain source for the
// concrete workspace associated with an agent run.
type WorkspaceContextSourceFactory interface {
	SourceForWorkspace(workspacePath string) projectbrain.Source
}

// WorkspaceContextCompiler combines static sources (for example a licensed
// external code-graph service) with workspace-local sources.
type WorkspaceContextCompiler struct {
	staticSources []projectbrain.Source
	factories     []WorkspaceContextSourceFactory
}

func NewWorkspaceContextCompiler(sources ...projectbrain.Source) *WorkspaceContextCompiler {
	return &WorkspaceContextCompiler{staticSources: append([]projectbrain.Source(nil), sources...)}
}

func (c *WorkspaceContextCompiler) WithSource(source projectbrain.Source) *WorkspaceContextCompiler {
	if c != nil && source != nil {
		c.staticSources = append(c.staticSources, source)
	}
	return c
}

func (c *WorkspaceContextCompiler) WithSourceFactory(factory WorkspaceContextSourceFactory) *WorkspaceContextCompiler {
	if c != nil && factory != nil {
		c.factories = append(c.factories, factory)
	}
	return c
}

// Compile preserves ProjectContextProvider compatibility for callers that only
// use static sources.
func (c *WorkspaceContextCompiler) Compile(ctx context.Context, req projectbrain.CompileRequest) (projectbrain.ContextPackage, error) {
	if c == nil {
		return projectbrain.NewCompiler().Compile(ctx, req)
	}
	return projectbrain.NewCompiler(c.staticSources...).Compile(ctx, req)
}

func (c *WorkspaceContextCompiler) CompileWorkspace(ctx context.Context, req projectbrain.CompileRequest, workspacePath string) (projectbrain.ContextPackage, error) {
	if c == nil {
		return projectbrain.NewCompiler().Compile(ctx, req)
	}
	sources := append([]projectbrain.Source(nil), c.staticSources...)
	for _, factory := range c.factories {
		if factory == nil {
			continue
		}
		if source := factory.SourceForWorkspace(workspacePath); source != nil {
			sources = append(sources, source)
		}
	}
	return projectbrain.NewCompiler(sources...).Compile(ctx, req)
}
