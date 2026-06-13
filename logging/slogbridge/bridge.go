// MODULE: logging/slogbridge
// PURPOSE: Adapt slog.Handler onto ion.Logger so slog producers (ThebeDB)
// are captured by the ion pipeline unchanged (every slog record maps to
// exactly one ion call at the equivalent level).
//
// CORE DATA STRUCTURES:
//   - Handler{lg, attrs, prefix}: immutable per the slog.Handler contract —
//     WithAttrs/WithGroup return copies; attrs is append-only, bounded by
//     the producer's attr count.
//
// TO MODIFY BEHAVIOR:
//   - Change level mapping: edit Handle's switch
//   - Change attr conversion: edit toField
//
// DO NOT:
//   - Filter levels here (return false from Enabled) — ion owns level
//     filtering; dropping records in the bridge hides them from every sink
//   - Mutate h.attrs in WithAttrs (handlers are shared across goroutines)
//
// EXTENSION POINT: any slog-speaking producer is captured by constructing
// slog.New(slogbridge.New(<ion child logger>)) — no producer changes.
package slogbridge

import (
	"context"
	"log/slog"
	"time"

	"github.com/neerajvipparla/ion"
)

// Handler forwards slog records to an ion.Logger.
type Handler struct {
	lg     ion.Logger
	attrs  []ion.Field
	prefix string // accumulated group path, "a.b." form
}

var _ slog.Handler = (*Handler)(nil)

// New wraps an ion logger as a slog handler. Pass a topic-scoped child
// (e.g. the "thebedb" logger) so bridged rows are attributable.
func New(lg ion.Logger) *Handler {
	return &Handler{lg: lg}
}

// Enabled always reports true: ion performs level filtering, and filtering
// here too would silently drop records from sinks with lower thresholds.
func (h *Handler) Enabled(context.Context, slog.Level) bool { return true }

// Handle maps one slog record to one ion call. The first error-valued attr
// becomes the err argument of Error/Critical; remaining attrs become fields.
//
// Time: O(a) where a = attr count (pre-bound + record); Space: O(a).
func (h *Handler) Handle(ctx context.Context, r slog.Record) error {
	fields := make([]ion.Field, 0, len(h.attrs)+r.NumAttrs())
	fields = append(fields, h.attrs...)

	var firstErr error
	r.Attrs(func(a slog.Attr) bool {
		if err, ok := a.Value.Resolve().Any().(error); ok && firstErr == nil {
			firstErr = err
			return true
		}
		fields = append(fields, h.toField(a))
		return true
	})

	switch {
	case r.Level >= slog.LevelError:
		h.lg.Error(ctx, r.Message, firstErr, fields...)
	case r.Level >= slog.LevelWarn:
		if firstErr != nil {
			fields = append(fields, ion.Err(firstErr))
		}
		h.lg.Warn(ctx, r.Message, fields...)
	case r.Level >= slog.LevelInfo:
		if firstErr != nil {
			fields = append(fields, ion.Err(firstErr))
		}
		h.lg.Info(ctx, r.Message, fields...)
	default:
		if firstErr != nil {
			fields = append(fields, ion.Err(firstErr))
		}
		h.lg.Debug(ctx, r.Message, fields...)
	}
	return nil
}

// WithAttrs returns a copy with the attrs pre-bound (immutability contract).
func (h *Handler) WithAttrs(attrs []slog.Attr) slog.Handler {
	next := &Handler{lg: h.lg, prefix: h.prefix}
	next.attrs = make([]ion.Field, 0, len(h.attrs)+len(attrs))
	next.attrs = append(next.attrs, h.attrs...)
	for _, a := range attrs {
		next.attrs = append(next.attrs, h.toField(a))
	}
	return next
}

// WithGroup returns a copy whose subsequent attr keys are prefixed with
// "name." — ion fields are flat, so groups become key prefixes.
func (h *Handler) WithGroup(name string) slog.Handler {
	if name == "" {
		return h
	}
	return &Handler{lg: h.lg, attrs: h.attrs, prefix: h.prefix + name + "."}
}

// toField converts one slog attr to an ion field, applying the group prefix.
func (h *Handler) toField(a slog.Attr) ion.Field {
	key := h.prefix + a.Key
	v := a.Value.Resolve()
	switch v.Kind() {
	case slog.KindString:
		return ion.String(key, v.String())
	case slog.KindInt64:
		return ion.Int64(key, v.Int64())
	case slog.KindUint64:
		return ion.Uint64(key, v.Uint64())
	case slog.KindFloat64:
		return ion.Float64(key, v.Float64())
	case slog.KindBool:
		return ion.Bool(key, v.Bool())
	case slog.KindDuration:
		return ion.Duration(key, v.Duration())
	case slog.KindTime:
		return ion.String(key, v.Time().UTC().Format(time.RFC3339Nano))
	default:
		// Groups and arbitrary values flatten to their string form: ion has
		// no Any constructor and ClickHouse columns are flat.
		return ion.String(key, v.String())
	}
}
