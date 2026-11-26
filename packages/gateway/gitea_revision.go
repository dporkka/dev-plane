package gateway

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
)

// GetBranch reads the named branch from the exact configured forge instance.
// Keep this optional capability separate from the legacy Forge contract.
func (f *GiteaForge) GetBranch(ctx context.Context, credential ForgeCredential, owner, name, branch string) (ForgeBranchRef, error) {
	var result struct {
		Name   string `json:"name"`
		Commit struct {
			ID string `json:"id"`
		} `json:"commit"`
	}
	if branch == "" {
		return ForgeBranchRef{}, fmt.Errorf("empty Gitea branch")
	}
	if err := f.do(ctx, credential, http.MethodGet, f.repoURL(owner, name)+"/branches/"+url.PathEscape(branch), nil, &result); err != nil {
		return ForgeBranchRef{}, err
	}
	if result.Name != branch || result.Commit.ID == "" {
		return ForgeBranchRef{}, fmt.Errorf("Gitea branch identity not confirmed")
	}
	return ForgeBranchRef{Ref: result.Name, SHA: result.Commit.ID}, nil
}

// GetPullRequest reads independent provider state after creation.
func (f *GiteaForge) GetPullRequest(ctx context.Context, credential ForgeCredential, owner, name string, number int) (*ForgePullRequest, error) {
	if number <= 0 {
		return nil, fmt.Errorf("invalid Gitea pull request number")
	}
	var result giteaPullRequest
	if err := f.do(ctx, credential, http.MethodGet, f.repoURL(owner, name)+"/pulls/"+strconv.Itoa(number), nil, &result); err != nil {
		return nil, err
	}
	if result.Number != number {
		return nil, fmt.Errorf("Gitea pull request identity not confirmed")
	}
	normalized := result.normalize()
	return &normalized, nil
}
