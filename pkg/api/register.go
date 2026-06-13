// MODULE: pkg/api/register.go
// PURPOSE: Handles POST /register — creates a user and returns their platform API key.
//          Owns key generation and one-time plaintext delivery.
//          Key is never stored in plaintext; SHA-256 hash is written to DB.
//
// CORE DATA STRUCTURES: none — stateless handler.
//
// TO MODIFY BEHAVIOR:
//   - Change key length: update the make([]byte, 32) size in Register().
//   - Add email validation: add a validator tag to the request struct.
//
// DO NOT:
//   - Log the generated key — it is the user's credential and must never appear in logs.
//   - Return the key in any subsequent endpoint — it is shown exactly once here.
package api

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/neerajvipparla/ion"
	"github.com/neerajvipparla/mcp-me/pkg/store"
)

type RegisterHandler struct {
	db store.UserDB
}

func NewRegisterHandler(db store.UserDB) *RegisterHandler {
	return &RegisterHandler{db: db}
}

func (h *RegisterHandler) Register(c *gin.Context) {
	var req struct {
		Email string `json:"email" binding:"required"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(400, gin.H{"error": err.Error()})
		return
	}

	ctx := c.Request.Context()
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		logger.Error(ctx, "key generation failed", err,
			ion.String("file", "register.go"),
			ion.String("func", "Register"),
			ion.String("op", "rand.Read"),
		)
		c.JSON(500, gin.H{"error": "key generation failed"})
		return
	}
	key := hex.EncodeToString(raw)

	hash := sha256.Sum256([]byte(key))
	keyHash := hex.EncodeToString(hash[:])

	if err := h.db.CreateUser(ctx, &store.UserRecord{
		ID:                 uuid.NewString(),
		Email:              req.Email,
		PlatformAPIKeyHash: keyHash,
	}); err != nil {
		logger.Error(ctx, "db error", err,
			ion.String("file", "register.go"),
			ion.String("func", "Register"),
			ion.String("op", "create user"),
			ion.String("email", req.Email),
		)
		c.JSON(500, gin.H{"error": "db error"})
		return
	}

	logger.Info(ctx, "user registered",
		ion.String("file", "register.go"),
		ion.String("func", "Register"),
		ion.String("email", req.Email),
	)

	// Plaintext key returned once — never stored, never logged.
	c.JSON(200, gin.H{
		"api_key": key,
		"email":   req.Email,
	})
}
