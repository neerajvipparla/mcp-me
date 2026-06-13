// MODULE: logging (AsyncLogger)
// PURPOSE: Own the single process-wide ion instance and the registry of
// topic-scoped child loggers (exactly one configured ion instance per process).
//
// CORE DATA STRUCTURES:
//   - AsyncLogger.Logging map[string]Logging: topic → child logger registry.
//     Key lookup, unordered, bounded by the topic constants (~10 entries);
//     guarded by mu for concurrent test access.
//
// TO MODIFY BEHAVIOR:
//   - Change sink configuration: edit logging/otelsetup (not here)
//   - Add a topic: add a constant in constants.go, call NamedLogger with it
//
// DO NOT:
//   - Construct AsyncLogger directly — NewAsyncLogger is the singleton gate;
//     a second ion instance doubles ClickHouse writers
//   - Store request/test-scoped state on AsyncLogger
//
// EXTENSION POINT: per-topic loggers via NamedLogger; sinks via otelsetup.
package logging

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/neerajvipparla/mcp-me/logging/otelsetup"

	"github.com/neerajvipparla/ion"
)

var (
	ionLogging_DIR      = "logs"
	ionLogging_FileName = ""
	once                sync.Once
	asyncLogger         *AsyncLogger
	mu                  sync.Mutex // guards asyncLogger.Logging
)

// Get returns the ion.Ion child logger for topic.
// Intended for package-level logger vars:
//
//	var logger = logging.Get(logging.TopicWorker)
//
// Safe to call from package-level var initialization — NewAsyncLogger's once.Do
// runs on first call, reading env vars that must already be set (the Makefile
// -include .env guarantees this for the server binary).
func Get(topic string) *ion.Ion {
	l, _ := NewAsyncLogger().NamedLogger(topic, "")
	if l == nil {
		return nil
	}
	return l.NamedLogger
}

// NewAsyncLogger returns the process-wide logger, initializing ion on first
// call. Init warnings (e.g. ClickHouse unreachable → export disabled) are
// logged at Warn level so a degraded run is visible in the remaining sinks.
// Panics only when no logger at all can be constructed.
func NewAsyncLogger() *AsyncLogger {
	once.Do(func() {
		asyncLogger = &AsyncLogger{Logging: make(map[string]Logging)}

		ionInstance, warnings, err := otelsetup.Setup(ionLogging_DIR, ionLogging_FileName)
		if err != nil {
			panic(fmt.Sprintf("FATAL: failed to set global logger: %v", err))
		}
		asyncLogger.GlobalLogger = ionInstance

		ctx := context.Background()
		for _, w := range warnings {
			ionInstance.Warn(ctx, "logging init degraded",
				ion.String("component", w.Component),
				ion.Err(w.Err),
			)
		}
	})
	return asyncLogger
}

// NamedLogger returns the child logger for topic, creating it on first use.
// The child is a real ion Child: the topic appears as the "logger" field in
// every row (queryable in ClickHouse), and Tracer()/Meter() stay available.
//
// Time: O(1) map lookup; Space: O(1) per new topic.
func (al *AsyncLogger) NamedLogger(topic string, fileName string) (*Logging, error) {
	if al.GlobalLogger == nil {
		return nil, fmt.Errorf("logging: global logger not initialized")
	}
	mu.Lock()
	defer mu.Unlock()
	if al.Logging == nil {
		al.Logging = make(map[string]Logging)
	}
	if existing, ok := al.Logging[topic]; ok && existing.NamedLogger != nil {
		return &existing, nil
	}
	al.Logging[topic] = Logging{
		Topic:       topic,
		FileName:    fileName,
		NamedLogger: al.GlobalLogger.Child(topic),
	}
	named := al.Logging[topic]
	return &named, nil
}

// GetNamedLogger returns an already-created topic logger.
func (al *AsyncLogger) GetNamedLogger(topic string) (*Logging, error) {
	mu.Lock()
	defer mu.Unlock()
	if al.Logging == nil {
		return nil, fmt.Errorf("logging: registry not initialized")
	}
	named, ok := al.Logging[topic]
	if !ok {
		return nil, fmt.Errorf("logging: named logger for topic %q not found", topic)
	}
	return &named, nil
}

// Sync flushes buffered log entries on the global instance.
func (al *AsyncLogger) Sync() error {
	if al.GlobalLogger == nil {
		return fmt.Errorf("logging: global logger not initialized")
	}
	return al.GlobalLogger.Sync()
}

// Shutdown flushes and closes all sinks, including the ClickHouse batch
// buffer — call it once at process exit (TestMain) or the tail of the run
// never reaches ClickHouse.
func (al *AsyncLogger) Shutdown() error {
	if al.GlobalLogger == nil {
		return fmt.Errorf("logging: global logger not initialized")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	return otelsetup.Shutdown(ctx, al.GlobalLogger)
}
