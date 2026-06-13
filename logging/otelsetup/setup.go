// MODULE: logging/otelsetup
// PURPOSE: Resolve environment + defaults into the single ion configuration
// for this process (config resolution happens here and nowhere else).
//
// CORE DATA STRUCTURES:
//   - package-level globals (globalLogger/globalWarnings/globalInitErr)
//     guarded by initOnce: write-once, read-many after Setup returns.
//
// TO MODIFY BEHAVIOR:
//   - Change a sink (console/file/ClickHouse): edit the cfg block in Setup
//   - Change the ClickHouse endpoint: set CLICKHOUSE_DSN before first use
//   - Add a new env override: extend Setup; keep resolution in this package
//
// DO NOT:
//   - Call ion.New anywhere else in this repo — two ion instances mean two
//     ClickHouse batch writers and undefined shutdown ordering
//   - Read env vars for logging config outside this package
//
// EXTENSION POINT: new sinks/levels are added inside Setup via ion.Config;
// callers are unaffected (they only ever see *ion.Ion).
package otelsetup

import (
	"context"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/neerajvipparla/mcp-me/logging/config"
	"github.com/neerajvipparla/ion"
)

var (
	initOnce       sync.Once
	globalLogger   *ion.Ion
	globalWarnings []ion.Warning
	globalInitErr  error
)

// Defaults. Override before the first Setup call (they are read once).
var (
	ServiceName = "ThebeDB-Testsuite"
	Version     = "0.0.1"
	Development = true
	Level       = "info"
	FileEnabled = false
	Filepath    = ""
)

// EnvClickHouseDSN and ClickHouseTable are re-exported for callers that
// only import this package (e.g. tests). The values live in config.
const (
	EnvClickHouseDSN = config.EnvClickHouseDSN
	ClickHouseTable  = config.ClickHouseTable
)

// ResolveClickHouseDSN returns the DSN to use given an env lookup function
// (os.LookupEnv in production; injected for tests). Resolution:
//
//	env set, non-empty → env value
//	env set, empty     → "" (ClickHouse disabled)
//	env unset          → default lab DSN
func ResolveClickHouseDSN(lookup func(string) (string, bool)) string {
	if v, ok := lookup(EnvClickHouseDSN); ok {
		return v
	}
	return config.DefaultClickHouseDSN
}

// Setup initializes ion exactly once and returns the process-wide instance.
// logDir/logFileName are fallbacks for file logging when FileEnabled is not
// explicitly configured. Warnings are non-fatal degradations (e.g. ClickHouse
// unreachable → export disabled, console/file keep working); callers should
// surface them, not ignore them.
func Setup(logDir string, logFileName string) (*ion.Ion, []ion.Warning, error) {
	initOnce.Do(func() {
		cfg := ion.Default()
		cfg.ServiceName = ServiceName
		cfg.Version = Version
		cfg.Development = Development
		// Expected errors (redelivery paths, fault-injection tests) must not
		// render as crashes — suppress dev-mode stack traces on Error logs.
		cfg.Level = Level

		cfg.Console = ion.ConsoleConfig{
			Enabled:        true,
			Format:         "pretty",
			Color:          true,
			ErrorsToStderr: true,
		}

		if FileEnabled && Filepath != "" {
			cfg.File = ion.FileConfig{
				Enabled:    true,
				Path:       Filepath,
				MaxSizeMB:  500,
				MaxAgeDays: 7,
				MaxBackups: 10,
				Compress:   true,
			}
		} else if logDir != "" && logFileName != "" {
			cfg.File = ion.FileConfig{
				Enabled: true,
				Path:    filepath.Join(logDir, logFileName),
			}
		} else {
			cfg.File = ion.FileConfig{Enabled: false}
		}

		// ClickHouse log sink (only when a DSN resolves). Explicit struct —
		// not WithClickHouse, which discards its own defaults (value-receiver
		// WithDefaults result is thrown away) and never sets AutoSchema, so a
		// fresh ClickHouse ends up with no table and silent insert failures.
		if dsn := ResolveClickHouseDSN(os.LookupEnv); dsn != "" {
			cfg.ClickHouse = ion.ClickHouseConfig{
				Enabled:       true,
				DSN:           dsn,
				Table:         ClickHouseTable,
				AutoSchema:    true,
				FlushInterval: 3 * time.Second,
			}
		}

		globalLogger, globalWarnings, globalInitErr = ion.New(cfg)
	})

	return globalLogger, globalWarnings, globalInitErr
}

// Shutdown gracefully shuts down ion, flushing pending logs (including the
// ClickHouse batch buffer — skipping this loses the tail of the run).
func Shutdown(ctx context.Context, ionInstance *ion.Ion) error {
	if ionInstance == nil {
		return nil
	}
	return ionInstance.Shutdown(ctx)
}
