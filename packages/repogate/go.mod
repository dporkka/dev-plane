module github.com/ai-dev-control-plane/repogate

go 1.25.11

require (
	github.com/ai-dev-control-plane/runtimes v0.0.0
	github.com/ai-dev-control-plane/vcs v0.0.0
	github.com/mattn/go-sqlite3 v1.14.47
)

replace (
	github.com/ai-dev-control-plane/runtimes => ../runtimes
	github.com/ai-dev-control-plane/vcs => ../vcs
)
