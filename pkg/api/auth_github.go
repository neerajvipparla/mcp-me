package api

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/neerajvipparla/ion"
	"github.com/neerajvipparla/mcp-me/pkg/store"
)

type GitHubAuthHandler struct {
	db store.UserDB
}

func NewGitHubAuthHandler(db store.UserDB) *GitHubAuthHandler {
	return &GitHubAuthHandler{db: db}
}

type githubUser struct {
	ID    int64  `json:"id"`
	Login string `json:"login"`
	Email string `json:"email"`
	Name  string `json:"name"`
}

func fetchGitHubUser(ctx context.Context, token string) (*githubUser, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "https://api.github.com/user", nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Accept", "application/vnd.github+json")

	client := &http.Client{Timeout: 8 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("github API returned %d", resp.StatusCode)
	}

	var u githubUser
	if err := json.NewDecoder(resp.Body).Decode(&u); err != nil {
		return nil, err
	}
	return &u, nil
}

// GitHubLogin validates a GitHub access token, then finds or creates the DocsMCP
// user and returns a fresh platform API key. Called by the Next.js frontend after
// Better Auth completes the OAuth dance.
//
// POST /v1/auth/github
// Body: { "github_token": "<access_token>" }
// Response: { "api_key": "<plaintext_key>", "email": "<email>" }
func (h *GitHubAuthHandler) GitHubLogin(c *gin.Context) {
	var req struct {
		GithubToken string `json:"github_token" binding:"required"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(400, gin.H{"error": "github_token required"})
		return
	}

	ctx := c.Request.Context()

	ghUser, err := fetchGitHubUser(ctx, req.GithubToken)
	if err != nil {
		logger.Warn(ctx, "github token validation failed",
			ion.String("file", "auth_github.go"),
			ion.String("func", "GitHubLogin"),
		)
		c.JSON(401, gin.H{"error": "invalid github token"})
		return
	}

	// Use GitHub-derived email; fall back to login@github if no public email
	email := ghUser.Email
	if email == "" {
		email = fmt.Sprintf("%s@github", ghUser.Login)
	}

	// Generate a fresh API key on every login (rotation on login is intentional —
	// the key cannot be recovered from the bcrypt hash so we always issue a new one)
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		logger.Error(ctx, "key generation failed", err,
			ion.String("file", "auth_github.go"),
			ion.String("func", "GitHubLogin"),
		)
		c.JSON(500, gin.H{"error": "key generation failed"})
		return
	}
	key := hex.EncodeToString(raw)
	hash := sha256.Sum256([]byte(key))
	keyHash := hex.EncodeToString(hash[:])

	// Upsert user: create if first login, update key hash if returning user.
	if err := h.db.UpsertUserByEmail(ctx, &store.UserRecord{
		ID:                 uuid.NewString(),
		Email:              email,
		PlatformAPIKeyHash: keyHash,
	}); err != nil {
		logger.Error(ctx, "upsert user failed", err,
			ion.String("file", "auth_github.go"),
			ion.String("func", "GitHubLogin"),
			ion.String("email", email),
		)
		c.JSON(500, gin.H{"error": "db error"})
		return
	}

	logger.Info(ctx, "github login",
		ion.String("file", "auth_github.go"),
		ion.String("func", "GitHubLogin"),
		ion.String("github_login", ghUser.Login),
		ion.String("email", email),
	)

	c.JSON(200, gin.H{"api_key": key, "email": email})
}
