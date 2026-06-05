package logger

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"strings"
	"sync"
	"time"
)

type Handler struct {
	opts   HandlerOptions
	writer io.Writer
	mu     sync.Mutex
}

type HandlerOptions struct {
	Level       slog.Leveler
	ReplaceAttr func(groups []string, a slog.Attr) slog.Attr
}

func NewHandler(w io.Writer, opts *HandlerOptions) *Handler {
	h := &Handler{writer: w}
	if opts != nil {
		h.opts = *opts
	}
	if h.opts.Level == nil {
		h.opts.Level = slog.LevelInfo
	}
	return h
}

func (h *Handler) Enabled(_ context.Context, level slog.Level) bool {
	return level >= h.opts.Level.Level()
}

func (h *Handler) Handle(_ context.Context, r slog.Record) error {
	var buf strings.Builder

	buf.WriteString(fmt.Sprintf("[%s]", r.Time.Format("15:04:05.000")))

	levelStr := strings.ToUpper(r.Level.String())
	buf.WriteString(fmt.Sprintf(" %-5s", levelStr))

	buf.WriteString(fmt.Sprintf(" %s", r.Message))

	r.Attrs(func(a slog.Attr) bool {
		if h.opts.ReplaceAttr != nil {
			a = h.opts.ReplaceAttr(nil, a)
		}
		if a.Equal(slog.Attr{}) {
			return true
		}
		buf.WriteString(fmt.Sprintf("  %s: %s", a.Key, formatValue(a.Value)))
		return true
	})

	buf.WriteByte('\n')

	h.mu.Lock()
	defer h.mu.Unlock()
	_, err := fmt.Fprint(h.writer, buf.String())
	return err
}

func (h *Handler) WithAttrs(attrs []slog.Attr) slog.Handler {
	return h
}

func (h *Handler) WithGroup(name string) slog.Handler {
	return h
}

func formatValue(v slog.Value) string {
	switch v.Kind() {
	case slog.KindString:
		return v.String()
	case slog.KindInt64:
		return fmt.Sprintf("%d", v.Int64())
	case slog.KindUint64:
		return fmt.Sprintf("%d", v.Uint64())
	case slog.KindFloat64:
		return fmt.Sprintf("%.2f", v.Float64())
	case slog.KindBool:
		return fmt.Sprintf("%t", v.Bool())
	case slog.KindDuration:
		return formatDuration(v.Duration())
	case slog.KindTime:
		return v.Time().Format(time.RFC3339)
	case slog.KindAny:
		return fmt.Sprintf("%v", v.Any())
	default:
		return v.String()
	}
}

func formatDuration(d time.Duration) string {
	switch {
	case d == 0:
		return "0s"
	case d < time.Microsecond:
		return fmt.Sprintf("%.0fns", float64(d))
	case d < time.Millisecond:
		return fmt.Sprintf("%.2fµs", float64(d)/1000)
	case d < time.Second:
		return fmt.Sprintf("%.2fms", float64(d)/float64(time.Millisecond))
	case d < time.Minute:
		return fmt.Sprintf("%.2fs", d.Seconds())
	default:
		return d.Round(time.Second).String()
	}
}
