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
	"fmt"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/neerajvipparla/ion"
	"github.com/neerajvipparla/mcp-me/logging"
	"github.com/neerajvipparla/mcp-me/pkg/store"
)

// logger is the shared structured logger for the api package.
var logger *ion.Ion = logging.Get(logging.TopicAPI)

// PlatformKeyAuth verifies the platform API key on every protected route.
// Accepts X-API-Key header or Authorization: Bearer <key>.
// Sets "user_id" in the Gin context on success.
func PlatformKeyAuth(db store.UserDB) gin.HandlerFunc {
	return func(c *gin.Context) {
		key := extractAPIKey(c)
		if key == "" {
			logger.Warn(c.Request.Context(), "auth failed: missing api key",
				ion.String("file", "middleware.go"),
				ion.String("func", "PlatformKeyAuth"),
				ion.String("path", c.FullPath()),
			)
			c.AbortWithStatusJSON(401, gin.H{"error": "missing api key"})
			return
		}
		userID, err := db.FindUserByKeyHash(c.Request.Context(), hashAPIKey(key))
		if err != nil || userID == "" {
			logger.Warn(c.Request.Context(), "auth failed: invalid api key",
				ion.String("file", "middleware.go"),
				ion.String("func", "PlatformKeyAuth"),
				ion.String("path", c.FullPath()),
			)
			c.AbortWithStatusJSON(401, gin.H{"error": "invalid api key"})
			return
		}
		c.Set("user_id", userID)
		c.Next()
	}
}

// IonLogger logs every HTTP request through ion so access logs reach ClickHouse
// alongside application logs. Call c.Next() first captures the actual response status.
func IonLogger() gin.HandlerFunc {
	return func(c *gin.Context) {
		start := time.Now()
		c.Next()
		latencyMs := float64(time.Since(start).Microseconds()) / 1000
		status := c.Writer.Status()
		logFn := logger.Info
		if status >= 400 {
			logFn = logger.Warn
		}
		logFn(c.Request.Context(), "request",
			ion.String("file", "middleware.go"),
			ion.String("func", "IonLogger"),
			ion.String("method", c.Request.Method),
			ion.String("path", c.Request.URL.Path),
			ion.String("status", fmt.Sprintf("%d", status)),
			ion.String("latency_ms", fmt.Sprintf("%.2f", latencyMs)),
			ion.String("ip", c.ClientIP()),
		)
	}
}

// IonRecovery catches panics, logs them through ion, and returns 500.
func IonRecovery() gin.HandlerFunc {
	return func(c *gin.Context) {
		defer func() {
			if r := recover(); r != nil {
				err, ok := r.(error)
				if !ok {
					err = fmt.Errorf("panic: %v", r)
				}
				logger.Error(c.Request.Context(), "panic recovered", err,
					ion.String("file", "middleware.go"),
					ion.String("func", "IonRecovery"),
					ion.String("path", c.Request.URL.Path),
				)
				c.AbortWithStatus(500)
			}
		}()
		c.Next()
	}
}

// CORS handles preflight OPTIONS requests and sets Access-Control-Allow-* headers.
// allowedOrigin should be set to the frontend URL (e.g. https://mcp-me-two.vercel.app).
func CORS(allowedOrigin string) gin.HandlerFunc {
	return func(c *gin.Context) {
		origin := c.GetHeader("Origin")
		if origin != "" && (origin == allowedOrigin || allowedOrigin == "*") {
			c.Header("Access-Control-Allow-Origin", origin)
		} else if allowedOrigin == "*" {
			c.Header("Access-Control-Allow-Origin", "*")
		} else {
			c.Header("Access-Control-Allow-Origin", allowedOrigin)
		}
		c.Header("Access-Control-Allow-Methods", "GET, POST, OPTIONS")
		c.Header("Access-Control-Allow-Headers", "Content-Type, Authorization, X-API-Key")
		c.Header("Access-Control-Max-Age", "86400")
		if c.Request.Method == "OPTIONS" {
			c.AbortWithStatus(204)
			return
		}
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
