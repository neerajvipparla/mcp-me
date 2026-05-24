// MODULE: pkg/qdrantcfg
// PURPOSE: Owns the single extension point for Qdrant deployment mode.
//          Store implementations receive a *qdrant.Client and are agnostic
//          to which Config produced it.
//
// TO ADD A NEW DEPLOYMENT MODE (e.g. mTLS, auth proxy):
//   - Implement Config interface with a new struct
//   - Update FromEnv() if you want env-var-driven selection
//   - No changes to any Store code required
//
// MIGRATION cloud → self-hosted:
//   Unset QDRANT_API_KEY, point QDRANT_HOST to your instance.
//   FromEnv() automatically selects SelfHostedConfig. Zero code changes.
package qdrantcfg

import (
	"os"
	"strconv"

	qdrantgo "github.com/qdrant/go-client/qdrant"
)

// Config is the connection strategy for a Qdrant deployment.
// Exported interface — implement to add new deployment modes.
type Config interface {
	Connect() (*qdrantgo.Client, error)
}

// CloudConfig connects to Qdrant Cloud over TLS using an API key.
// This is the default production mode.
type CloudConfig struct {
	Host   string // cluster hostname, e.g. xyz.us-east-1-0.aws.cloud.qdrant.io
	Port   int    // gRPC port, typically 6334
	APIKey string
}

func (c CloudConfig) Connect() (*qdrantgo.Client, error) {
	return qdrantgo.NewClient(&qdrantgo.Config{
		Host:   c.Host,
		Port:   c.Port,
		APIKey: c.APIKey,
		UseTLS: true,
	})
}

// SelfHostedConfig connects to a local or on-prem Qdrant instance without TLS.
// Use for docker-compose dev setups or private deployments.
// Swap CloudConfig for SelfHostedConfig to migrate — no other code changes needed.
type SelfHostedConfig struct {
	Host string // e.g. localhost
	Port int    // gRPC port, typically 6334
}

func (c SelfHostedConfig) Connect() (*qdrantgo.Client, error) {
	return qdrantgo.NewClient(&qdrantgo.Config{
		Host:   c.Host,
		Port:   c.Port,
		UseTLS: false,
	})
}

// NewClient creates a Qdrant client from any Config implementation.
// All store constructors call this — they never inspect the Config type.
func NewClient(cfg Config) (*qdrantgo.Client, error) {
	return cfg.Connect()
}

// From builds a Config from explicit values — use when host/port come from
// config.yaml and apiKey from an env var.
// If apiKey is empty → SelfHostedConfig; otherwise → CloudConfig.
func From(host string, port int, apiKey string) Config {
	if apiKey != "" {
		return CloudConfig{Host: host, Port: port, APIKey: apiKey}
	}
	return SelfHostedConfig{Host: host, Port: port}
}

// FromEnv selects the deployment mode from environment variables.
//
//	QDRANT_API_KEY set   → CloudConfig  (Qdrant Cloud, TLS)
//	QDRANT_API_KEY unset → SelfHostedConfig (local / on-prem, no TLS)
//
// Required env vars for cloud mode:
//
//	QDRANT_HOST    cluster hostname (e.g. xyz.us-east-1-0.aws.cloud.qdrant.io)
//	QDRANT_API_KEY cloud API key
//	QDRANT_PORT    gRPC port (default: 6334)
// FromEnv is a convenience wrapper that reads QDRANT_HOST, QDRANT_PORT,
// and QDRANT_API_KEY directly from the environment.
// Prefer From() when host/port already come from config.yaml.
func FromEnv() Config {
	return From(
		getEnv("QDRANT_HOST", "localhost"),
		getEnvInt("QDRANT_PORT", 6334),
		os.Getenv("QDRANT_API_KEY"),
	)
}

func getEnv(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

func getEnvInt(key string, def int) int {
	if v := os.Getenv(key); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			return n
		}
	}
	return def
}
