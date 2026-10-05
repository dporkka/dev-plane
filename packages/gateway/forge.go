package gateway

import (
	"context"
	"errors"
	"time"
)

// ErrUnsupportedForgeCapability is returned when a forge cannot preserve a
// requested semantic. Callers should fail closed rather than silently weaken
// intent (for example, creating a normal pull request when a draft was asked for).
var ErrUnsupportedForgeCapability = errors.New("unsupported forge capability")

// ForgeAuthority identifies the exact credential-bearing forge instance.
// BaseURL is the canonical instance root, never an API endpoint.
type ForgeAuthority struct {
	Provider string
	BaseURL  string
}

// ForgeAuthorityProvider is implemented by production adapters that can bind
// credentials to one exact forge instance. Security-sensitive callers should
// fail closed when an adapter cannot provide this identity.
type ForgeAuthorityProvider interface {
	Authority() ForgeAuthority
}

// ForgeCredential contains the credential material required by a forge adapter.
// It is intentionally small so higher layers do not depend on provider-specific
// OAuth or personal-access-token types.
type ForgeCredential struct {
	AccessToken string
}

// ForgeRepository is the provider-neutral repository shape needed by Dev Plane.
type ForgeRepository struct {
	ID            int64
	Name          string
	FullName      string
	Description   string
	Private       bool
	CloneURL      string
	SSHURL        string
	HTMLURL       string
	DefaultBranch string
	PushedAt      time.Time
}

// ForgeBranchRef identifies one pull-request branch and its immutable head when
// the forge returned one.
type ForgeBranchRef struct {
	Ref string
	SHA string
}

// ForgePullRequest is the provider-neutral pull-request shape needed by Dev Plane.
type ForgePullRequest struct {
	ID        int64
	Number    int
	Title     string
	Body      string
	State     string
	HTMLURL   string
	Draft     bool
	Head      ForgeBranchRef
	Base      ForgeBranchRef
	CreatedAt time.Time
	UpdatedAt time.Time
}

// ForgeNewPullRequest is the common pull-request creation contract.
type ForgeNewPullRequest struct {
	Title string
	Body  string
	Head  string
	Base  string
	Draft bool
}

// ForgeMergeRequest contains merge semantics that both GitHub and Gitea can
// preserve. ExpectedHeadSHA binds a merge to the reviewed pull-request head.
type ForgeMergeRequest struct {
	Method          string
	Title           string
	Message         string
	ExpectedHeadSHA string
}

// ForgeMergeResult is the normalized result of a merge request. Some forges do
// not return the merge commit SHA in the merge response, so SHA may be empty.
type ForgeMergeResult struct {
	SHA     string
	Merged  bool
	Message string
}

// Forge is the narrow VCS-host boundary used by Dev Plane. It intentionally
// covers only the repository/PR/webhook capabilities currently required by the
// control plane; provider-specific features remain outside this contract until
// they have a clear cross-forge semantic.
type Forge interface {
	Name() string
	ListRepositories(ctx context.Context, credential ForgeCredential, page int) ([]ForgeRepository, error)
	GetRepository(ctx context.Context, credential ForgeCredential, owner, name string) (*ForgeRepository, error)
	CreatePullRequest(ctx context.Context, credential ForgeCredential, owner, name string, pr ForgeNewPullRequest) (*ForgePullRequest, error)
	MergePullRequest(ctx context.Context, credential ForgeCredential, owner, name string, number int, req ForgeMergeRequest) (*ForgeMergeResult, error)
	CreateWebhook(ctx context.Context, credential ForgeCredential, owner, name, callbackURL, secret string) (int64, error)
	DeleteWebhook(ctx context.Context, credential ForgeCredential, owner, name string, hookID int64) error
}
