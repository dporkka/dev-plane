package gateway

import (
	"context"
	"fmt"
	"net/http"
	"strconv"
)

// GiteaMergeSnapshot includes repository IDs, which distinguish a same-name
// branch in a fork from the branch in the bound repository.
type GiteaMergeSnapshot struct {
	ID      int64         `json:"id"`
	Number  int           `json:"number"`
	State   string        `json:"state"`
	Draft   bool          `json:"draft"`
	Merged  bool          `json:"merged"`
	HTMLURL string        `json:"html_url"`
	Head    GiteaMergeRef `json:"head"`
	Base    GiteaMergeRef `json:"base"`
}

type GiteaMergeRef struct {
	Ref  string `json:"ref"`
	SHA  string `json:"sha"`
	Repo struct {
		ID int64 `json:"id"`
	} `json:"repo"`
}

func (f *GiteaForge) GetMergeSnapshot(ctx context.Context, cred ForgeCredential, owner, name string, number int) (GiteaMergeSnapshot, error) {
	var result GiteaMergeSnapshot
	if number <= 0 {
		return result, fmt.Errorf("invalid Gitea PR number")
	}
	if err := f.do(ctx, cred, http.MethodGet, f.repoURL(owner, name)+"/pulls/"+strconv.Itoa(number), nil, &result); err != nil {
		return GiteaMergeSnapshot{}, err
	}
	if result.Number != number || result.ID <= 0 || result.Head.Repo.ID <= 0 || result.Base.Repo.ID <= 0 {
		return GiteaMergeSnapshot{}, fmt.Errorf("Gitea PR repository identity unavailable")
	}
	return result, nil
}
