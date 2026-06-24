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

// RotateKey generates a new platform API key unconditionally, replacing the old one.
// POST /v1/auth/github/rotate
func (h *GitHubAuthHandler) RotateKey(c *gin.Context) {
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
		c.JSON(401, gin.H{"error": "invalid github token"})
		return
	}

	email := ghUser.Email
	if email == "" {
		email = fmt.Sprintf("%s@github", ghUser.Login)
	}

	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		c.JSON(500, gin.H{"error": "key generation failed"})
		return
	}
	key := hex.EncodeToString(raw)
	hash := sha256.Sum256([]byte(key))
	keyHash := hex.EncodeToString(hash[:])

	if err := h.db.RotateUserKey(ctx, email, keyHash); err != nil {
		logger.Error(ctx, "rotate key failed", err,
			ion.String("file", "auth_github.go"),
			ion.String("func", "RotateKey"),
			ion.String("email", email),
		)
		c.JSON(500, gin.H{"error": "db error"})
		return
	}

	logger.Info(ctx, "api key rotated",
		ion.String("file", "auth_github.go"),
		ion.String("func", "RotateKey"),
		ion.String("github_login", ghUser.Login),
	)

	c.JSON(200, gin.H{"api_key": key, "email": email})
}

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

	hasKey, err := h.db.UpsertUserByEmail(ctx, &store.UserRecord{
		ID:                 uuid.NewString(),
		Email:              email,
		PlatformAPIKeyHash: keyHash,
	})
	if err != nil {
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
		ion.String("has_key", fmt.Sprintf("%v", hasKey)),
	)

	if hasKey {
		// User already has a key stored — we can't recover it, don't overwrite it.
		c.JSON(200, gin.H{"has_key": true, "email": email})
		return
	}
	c.JSON(200, gin.H{"api_key": key, "email": email})
}
