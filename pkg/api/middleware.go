// MODULE: pkg/api/middleware.go
// PURPOSE: Authenticates HTTP requests using the platform API key.
//          Owns the key extraction and SHA-256 hash lookup against UserDB.
//
// CORE DATA STRUCTURES: none — stateless handler, no retained state.
//
// TO MODIFY BEHAVIOR:
//   - Add a new header: extend extractAPIKey().
//   - Change hash algorithm: update hashAPIKey() — requires a DB migration to rehash all keys.
//
// DO NOT:
//   - Use bcrypt here — platform_api_key is SHA-256 (deterministic → DB lookup works).
//     bcrypt is only for mcp_api_key (verified once, never looked up by hash).
//   - Import *PostgresStore — accepts store.UserDB so any compatible backend works.
package api

import (
	"crypto/sha256"
	"encoding/hex"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/neerajvipparla/mcp-me/pkg/store"
)

// PlatformKeyAuth verifies the platform API key on every protected route.
// Accepts X-API-Key header or Authorization: Bearer <key>.
// Sets "user_id" in the Gin context on success.
func PlatformKeyAuth(db store.UserDB) gin.HandlerFunc {
	return func(c *gin.Context) {
		key := extractAPIKey(c)
		if key == "" {
			c.AbortWithStatusJSON(401, gin.H{"error": "missing api key"})
			return
		}
		userID, err := db.FindUserByKeyHash(c.Request.Context(), hashAPIKey(key))
		if err != nil || userID == "" {
			c.AbortWithStatusJSON(401, gin.H{"error": "invalid api key"})
			return
		}
		c.Set("user_id", userID)
		c.Next()
	}
}

func extractAPIKey(c *gin.Context) string {
	if k := c.GetHeader("X-API-Key"); k != "" {
		return k
	}
	return strings.TrimPrefix(c.GetHeader("Authorization"), "Bearer ")
}

func hashAPIKey(key string) string {
	h := sha256.Sum256([]byte(key))
	return hex.EncodeToString(h[:])
}
