// MODULE: pkg/config/config.go
// PURPOSE: Loads config.yaml into a typed Config struct.
//          Owns the boundary between non-secret settings (yaml) and secrets (env vars).
//          Secrets are never stored in config.yaml — they are read from the environment
//          at the call sites that need them (e.g. DSN(), Qdrant API key).
//
// CORE DATA STRUCTURES:
//   - Config (value struct): loaded once at startup, read-only after that.
//     Passed by pointer so nil-check detects a missing Load() call.
//
// TO MODIFY BEHAVIOR:
//   - Add a config field: extend the relevant sub-struct and add the yaml tag.
//   - Change config file path: update the path passed to Load() in main.go.
//
// DO NOT:
//   - Store secrets (API keys, passwords) in config.yaml or in this struct.
//   - Call os.Getenv here — secret injection happens at the call site only.
//
// EXTENSION POINT: add sub-structs per new service; register them under Config.
package config

import (
	"fmt"
	"os"

	"gopkg.in/yaml.v3"
)

// Config holds all non-secret application settings loaded from config.yaml.
type Config struct {
	Server   ServerConfig   `yaml:"server"`
	Qdrant   QdrantConfig   `yaml:"qdrant"`
	Postgres PostgresConfig `yaml:"postgres"`
	Crawler  CrawlerConfig  `yaml:"crawler"`
	Worker   WorkerConfig   `yaml:"worker"`
}

type ServerConfig struct {
	Port int    `yaml:"port"`
	Host string `yaml:"host"`
}

// ResolvedHost returns the public base URL for this server.
// SERVER_HOST env var takes full precedence (production override).
// Falls back to the yaml value, which defaults to http://localhost:8080.
func (s ServerConfig) ResolvedHost() string {
	if h := os.Getenv("SERVER_HOST"); h != "" {
		return h
	}
	return s.Host
}

// QdrantConfig holds connection settings only — no API key (secret, lives in env).
// Whether CloudConfig or SelfHostedConfig is selected is determined by the presence
// of QDRANT_API_KEY at runtime (see pkg/qdrantcfg).
type QdrantConfig struct {
	Host string `yaml:"host"`
	Port int    `yaml:"port"`
}

// ResolvedHost returns the effective Qdrant host.
// QDRANT_HOST env var takes full precedence when set (same pattern as DATABASE_URL).
func (q QdrantConfig) ResolvedHost() string {
	if h := os.Getenv("QDRANT_HOST"); h != "" {
		return h
	}
	return q.Host
}

type PostgresConfig struct {
	Host string `yaml:"host"`
	Port int    `yaml:"port"`
	DB   string `yaml:"db"`
	User string `yaml:"user"`
}

// DSN builds the Postgres connection string.
// DATABASE_URL env var takes full precedence when set (production override).
// Otherwise combines config fields with the injected password secret.
func (p PostgresConfig) DSN(password string) string {
	if url := os.Getenv("DATABASE_URL"); url != "" {
		return url
	}
	return fmt.Sprintf(
		"postgres://%s:%s@%s:%d/%s?sslmode=disable",
		p.User, password, p.Host, p.Port, p.DB,
	)
}

type CrawlerConfig struct {
	MaxPages    int `yaml:"max_pages"`
	Concurrency int `yaml:"concurrency"`
}

type WorkerConfig struct {
	Concurrency int `yaml:"concurrency"`
	BatchSize   int `yaml:"batch_size"`
}

// Load reads and decodes the YAML file at path into a Config.
func Load(path string) (*Config, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("config: open %s: %w", path, err)
	}
	defer f.Close()

	var cfg Config
	if err := yaml.NewDecoder(f).Decode(&cfg); err != nil {
		return nil, fmt.Errorf("config: decode: %w", err)
	}
	return &cfg, nil
}
