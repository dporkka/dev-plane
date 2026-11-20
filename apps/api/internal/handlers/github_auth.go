package handlers

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/google/uuid"
	"golang.org/x/oauth2"
	"golang.org/x/oauth2/github"

	"github.com/ai-dev-control-plane/api/internal/auth"
	"github.com/ai-dev-control-plane/api/internal/config"
	"github.com/ai-dev-control-plane/api/internal/respond"
)

// GitHubAuthHandler handles GitHub OAuth authentication.
type GitHubAuthHandler struct {
	db     *sql.DB
	config *config.Config
	oauth  *oauth2.Config
}

// NewGitHubAuthHandler creates a new GitHub auth handler.
func NewGitHubAuthHandler(db *sql.DB, cfg *config.Config) *GitHubAuthHandler {
	oauthCfg := &oauth2.Config{
		ClientID:     cfg.GitHubClientID,
		ClientSecret: cfg.GitHubSecret,
		Endpoint:     github.Endpoint,
		Scopes:       []string{"repo", "read:org", "user:email"},
	}
	return &GitHubAuthHandler{
		db:     db,
		config: cfg,
		oauth:  oauthCfg,
	}
}

// GitHubAuthRedirect redirects the user to GitHub for OAuth authentication.
func (h *GitHubAuthHandler) GitHubAuthRedirect(w http.ResponseWriter, r *http.Request) {
	if h.config.GitHubClientID == "" {
		respond.Error(w, http.StatusServiceUnavailable, errors.New("GitHub OAuth not configured"))
		return
	}

	state := uuid.New().String()

	// Store state in cookie
	http.SetCookie(w, &http.Cookie{
		Name:     "oauth_state",
		Value:    state,
		Path:     "/",
		MaxAge:   600,
		HttpOnly: true,
		Secure:   h.config.OAuthCookieSecure,
		SameSite: http.SameSiteStrictMode,
	})

	url := h.oauth.AuthCodeURL(state)
	http.Redirect(w, r, url, http.StatusTemporaryRedirect)
}

// GitHubAuthCallback handles the GitHub OAuth callback.
func (h *GitHubAuthHandler) GitHubAuthCallback(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	// Verify state
	cookie, err := r.Cookie("oauth_state")
	if err != nil {
		respond.Error(w, http.StatusBadRequest, errors.New("oauth state not found"))
		return
	}
	if r.URL.Query().Get("state") != cookie.Value {
		respond.Error(w, http.StatusBadRequest, errors.New("invalid oauth state"))
		return
	}

	code := r.URL.Query().Get("code")
	if code == "" {
		respond.Error(w, http.StatusBadRequest, errors.New("authorization code not provided"))
		return
	}

	// Exchange code for token
	token, err := h.oauth.Exchange(ctx, code)
	if err != nil {
		respond.Error(w, http.StatusInternalServerError, fmt.Errorf("failed to exchange code: %w", err))
		return
	}

	// Get GitHub user profile
	client := h.oauth.Client(ctx, token)
	resp, err := client.Get("https://api.github.com/user")
	if err != nil {
		respond.Error(w, http.StatusInternalServerError, fmt.Errorf("failed to get user profile: %w", err))
		return
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		respond.Error(w, http.StatusInternalServerError, fmt.Errorf("github api returned status %d", resp.StatusCode))
		return
	}

	var ghUser githubOAuthProfile
	if err := json.NewDecoder(resp.Body).Decode(&ghUser); err != nil {
		respond.Error(w, http.StatusInternalServerError, fmt.Errorf("failed to decode user profile: %w", err))
		return
	}

	// Get primary email if email is not public
	if ghUser.Email == "" {
		emailResp, err := client.Get("https://api.github.com/user/emails")
		if err == nil && emailResp.StatusCode == http.StatusOK {
			var emails []struct {
				Email    string `json:"email"`
				Primary  bool   `json:"primary"`
				Verified bool   `json:"verified"`
			}
			json.NewDecoder(emailResp.Body).Decode(&emails)
			emailResp.Body.Close()
			for _, e := range emails {
				if e.Primary && e.Verified {
					ghUser.Email = e.Email
					break
				}
			}
		}
	}

	if ghUser.Email == "" {
		respond.Error(w, http.StatusBadRequest, errors.New("no verified email found"))
		return
	}

	// Resolve the immutable GitHub subject to its verified persisted tenant
	// and role. Never choose a tenant from the mutable GitHub username.
	principal, err := resolveGitHubOAuthPrincipal(ctx, h.db, ghUser)
	if err != nil {
		respond.Error(w, http.StatusForbidden, errors.New("GitHub identity has no active, unambiguous membership"))
		return
	}

	// JWT claims must reflect the persisted membership, never a hard-coded owner.
	jwtToken, err := auth.GenerateToken(principal.UserID, principal.OrgID, ghUser.Email, principal.Role, h.config.JWTSecret, 60*time.Minute)
	if err != nil {
		respond.Error(w, http.StatusInternalServerError, fmt.Errorf("failed to generate token: %w", err))
		return
	}

	// Clear oauth state cookie
	http.SetCookie(w, &http.Cookie{
		Name:     "oauth_state",
		Value:    "",
		Path:     "/",
		MaxAge:   -1,
		HttpOnly: true,
	})

	respond.JSON(w, http.StatusOK, map[string]string{
		"token": jwtToken,
		"email": ghUser.Email,
	})
}
