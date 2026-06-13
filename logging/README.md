# Logging Module

Structured logging for ThebeDB-Testsuite, built on
[ion](https://github.com/JupiterMetaLabs/ion) (local fork via `replace ../ion`).
Every log row goes to the console (pretty, colored) and — when reachable —
to ClickHouse (`ion_testsuite_logs` table), where runs can be queried and fed to a model.

## Topology

```
producers                    logging package                sinks
─────────                    ───────────────                ─────
suite/tests ── ion.Logger ─┐
                           ├─ AsyncLogger (one ion.Ion) ──┬─ console (pretty)
ThebeDB (slog) ─ slogbridge┘                              ├─ file (optional)
                                                          └─ ClickHouse ion_testsuite_logs
```

## Components

| File | Owns |
|---|---|
| `interface.go` | `LoggingInterface`, `AsyncLogger`, `Logging` types |
| `ion_Builder.go` | Singleton construction + topic registry (`NewAsyncLogger`, `NamedLogger`) |
| `logging_builder.go` | Optional builder for `Logging` values with metadata |
| `constants.go` | Topic constants (`TopicInfra`, `TopicE2E`, `TopicBench`, `TopicThebeDB`) |
| `otelsetup/setup.go` | Env/default → ion config resolution; `Setup` / `Shutdown` |
| `slogbridge/bridge.go` | `slog.Handler` adapter so slog producers (ThebeDB) flow into ion |

## Usage

```go
al := logging.NewAsyncLogger()                       // singleton; safe to call anywhere
lg, err := al.NamedLogger(logging.TopicInfra, "")    // topic = "logger" field in rows
lg.NamedLogger.Info(ctx, "suite ready", ion.String("container", id))

// Capture a slog producer (e.g. ThebeDB):
slog.SetDefault(slog.New(slogbridge.New(thebeLogger)))

// At process exit — flushes the ClickHouse batch buffer:
defer al.Shutdown()
```

## Configuration

| Knob | Where | Default |
|---|---|---|
| ClickHouse DSN | `CLICKHOUSE_DSN` env | internal lab endpoint (`192.168.200.7:8123`) |
| Disable ClickHouse | `CLICKHOUSE_DSN=""` (set but empty) | — |
| Level / service name / file sink | `otelsetup` package vars, before first `NewAsyncLogger` | `info`, `ThebeDB-Testsuite`, file off |

ClickHouse being unreachable is non-fatal: ion emits a warning (logged at
Warn), disables the exporter, and console/file logging continues.

## Querying a run

```sql
SELECT timestamp, level, logger, message
FROM ion_testsuite_logs
WHERE service = 'ThebeDB-Testsuite'
ORDER BY timestamp DESC
LIMIT 100
```

Filter by subsystem with `logger = 'infra' | 'e2e' | 'bench' | 'thebedb'`.
