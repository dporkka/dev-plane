package gateway

import (
	"context"

	"golang.org/x/oauth2"
)

// GitHubForge adapts the existing GitHub gateway to the provider-neutral Forge
// contract without changing the current GitHub-specific API surface.
type GitHubForge struct {
	gateway *GitHubGateway
}

var _ Forge = (*GitHubForge)(nil)

func NewGitHubForge(gateway *GitHubGateway) *GitHubForge {
	return &GitHubForge{gateway: gateway}
}

func (f *GitHubForge) Name() string { return "github" }

func (f *GitHubForge) ListRepositories(ctx context.Context, credential ForgeCredential, page int) ([]ForgeRepository, error) {
	repos, err := f.gateway.ListRepos(ctx, githubForgeToken(credential), page)
	if err != nil {
		return nil, err
	}
	out := make([]ForgeRepository, 0, len(repos))
	for _, repo := range repos {
		out = append(out, normalizeGitHubRepository(repo))
	}
	return out, nil
}

func (f *GitHubForge) GetRepository(ctx context.Context, credential ForgeCredential, owner, name string) (*ForgeRepository, error) {
	repo, err := f.gateway.GetRepo(ctx, githubForgeToken(credential), owner, name)
	if err != nil {
		return nil, err
	}
	normalized := normalizeGitHubRepository(*repo)
	return &normalized, nil
}

func (f *GitHubForge) CreatePullRequest(ctx context.Context, credential ForgeCredential, owner, name string, pr ForgeNewPullRequest) (*ForgePullRequest, error) {
	created, err := f.gateway.CreatePR(ctx, githubForgeToken(credential), owner, name, NewPR{
		Title: pr.Title,
		Body:  pr.Body,
		Head:  pr.Head,
		Base:  pr.Base,
		Draft: pr.Draft,
	})
	if err != nil {
		return nil, err
	}
	normalized := normalizeGitHubPullRequest(*created)
	return &normalized, nil
}

func (f *GitHubForge) MergePullRequest(ctx context.Context, credential ForgeCredential, owner, name string, number int, req ForgeMergeRequest) (*ForgeMergeResult, error) {
	result, err := f.gateway.MergePR(ctx, githubForgeToken(credential), owner, name, number, MergePRRequest{
		Method:  req.Method,
		Title:   req.Title,
		Message: req.Message,
		SHA:     req.ExpectedHeadSHA,
	})
	if err != nil {
		return nil, err
	}
	return &ForgeMergeResult{
		SHA:     result.SHA,
		Merged:  result.Merged,
		Message: result.Message,
	}, nil
}

func (f *GitHubForge) CreateWebhook(ctx context.Context, credential ForgeCredential, owner, name, callbackURL, secret string) (int64, error) {
	return f.gateway.CreateWebhook(ctx, githubForgeToken(credential), owner, name, callbackURL, secret)
}

func (f *GitHubForge) DeleteWebhook(ctx context.Context, credential ForgeCredential, owner, name string, hookID int64) error {
	return f.gateway.DeleteWebhook(ctx, githubForgeToken(credential), owner, name, hookID)
}

func githubForgeToken(credential ForgeCredential) *oauth2.Token {
	return &oauth2.Token{AccessToken: credential.AccessToken}
}

func normalizeGitHubRepository(repo GitHubRepo) ForgeRepository {
	return ForgeRepository{
		ID:            repo.ID,
		Name:          repo.Name,
		FullName:      repo.FullName,
		Description:   repo.Description,
		Private:       repo.Private,
		CloneURL:      repo.CloneURL,
		SSHURL:        repo.SSHURL,
		HTMLURL:       repo.HTMLURL,
		DefaultBranch: repo.DefaultBranch,
		PushedAt:      repo.PushedAt,
	}
}

func normalizeGitHubPullRequest(pr GitHubPR) ForgePullRequest {
	return ForgePullRequest{
		ID:        pr.ID,
		Number:    pr.Number,
		Title:     pr.Title,
		Body:      pr.Body,
		State:     pr.State,
		HTMLURL:   pr.HTMLURL,
		Head:      ForgeBranchRef{Ref: pr.Head.Ref, SHA: pr.Head.SHA},
		Base:      ForgeBranchRef{Ref: pr.Base.Ref, SHA: pr.Base.SHA},
		CreatedAt: pr.CreatedAt,
		UpdatedAt: pr.UpdatedAt,
	}
}
