// Package config is the single source of truth for all external connection
// config: DSNs, credentials, file paths, and the env var names that override them.
//
// To change any connection detail, edit this file only — every other package
// imports from here.
package config

// ── ClickHouse ────────────────────────────────────────────────────────────────

// EnvClickHouseDSN overrides the ClickHouse DSN. Setting it to an empty
// string (exported but empty) disables ClickHouse export entirely.
const EnvClickHouseDSN = "CLICKHOUSE_DSN"

// ClickHouseTable is this repo's log table (per-service convention used
// across JM services, e.g. ion_fastsync_logs).
const ClickHouseTable = "ion_testsuite_logs"

// DefaultClickHouseDSN is the internal lab endpoint. Logs flow with zero
// setup on lab machines; production deployments should set EnvClickHouseDSN.
const DefaultClickHouseDSN = "http://default:password@192.168.100.1:8123/default"

// LocalClickHouseDSN is the DSN for the ClickHouse started by `make infra-up`.
const LocalClickHouseDSN = "http://default:password@192.168.100.1:8123/default"

