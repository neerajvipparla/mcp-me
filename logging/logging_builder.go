package logging

import (
	"context"
	"fmt"
	"time"

	"github.com/neerajvipparla/ion"
)

// NewLoggingBuilder builds a Logging value step by step. Prefer
// AsyncLogger.NamedLogger for normal use — this builder exists for callers
// that need metadata (DIR/KeepLogs) attached before constructing the child.
func NewLoggingBuilder() *Logging {
	return &Logging{}
}

func (lb *Logging) SetFileName(fileName string) *Logging {
	lb.FileName = fileName
	return lb
}

func (lb *Logging) SetTopic(topic string) *Logging {
	lb.Topic = topic
	return lb
}

// NewNamedLogger derives the topic-scoped child from the global instance.
// Child (not Named) keeps the concrete *ion.Ion so Tracer()/Meter() work.
func (lb *Logging) NewNamedLogger(globalLogger *ion.Ion) *Logging {
	lb.NamedLogger = globalLogger.Child(lb.Topic)
	return lb
}

func (lb *Logging) GetNamedLogger() *ion.Ion {
	return lb.NamedLogger
}

func (lb *Logging) GetLoggingMetadata() *LoggingMetadata {
	return lb.LoggingMetadata
}

func (lb *Logging) SetLoggingMetadata(loggingMetadata *LoggingMetadata) *Logging {
	lb.LoggingMetadata = loggingMetadata
	return lb
}

func (lb *Logging) Close() error {
	if lb.NamedLogger == nil {
		return fmt.Errorf("NamedLogger is not initialized")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	return lb.NamedLogger.Shutdown(ctx)
}

func (lb *Logging) Sync() error {
	if lb.NamedLogger == nil {
		return fmt.Errorf("NamedLogger is not initialized")
	}
	return lb.NamedLogger.Sync()
}
