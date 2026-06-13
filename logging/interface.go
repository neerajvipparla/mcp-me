package logging

import "github.com/neerajvipparla/ion"

// LoggingInterface is the surface callers use: obtain a topic-scoped logger,
// flush, and shut down. Kept to exactly what consumers call (ISP) —
// construction itself goes through NewAsyncLogger.
type LoggingInterface interface {
	NamedLogger(topic string, fileName string) (*Logging, error)
	GetNamedLogger(topic string) (*Logging, error)
	Sync() error
	Shutdown() error
}

// Compile-time check: AsyncLogger satisfies the interface.
var _ LoggingInterface = (*AsyncLogger)(nil)

// AsyncLogger owns the process-wide ion instance and the registry of
// topic-scoped child loggers. Construct via NewAsyncLogger (singleton).
type AsyncLogger struct {
	GlobalLogger *ion.Ion
	Logging      map[string]Logging
}

// Logging is one topic-scoped logger. NamedLogger is an ion Child of the
// global instance: the topic shows up as the "logger" field in every row.
type Logging struct {
	Topic           string
	FileName        string
	NamedLogger     *ion.Ion
	LoggingMetadata *LoggingMetadata
}

type LoggingMetadata struct {
	DIR      string
	KeepLogs bool
}
