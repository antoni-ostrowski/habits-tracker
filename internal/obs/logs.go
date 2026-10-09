package obs

import (
	"context"
	"log/slog"
	"os"
	"strings"

	"go.opentelemetry.io/contrib/bridges/otelslog"
)

// MinLevel drops records below Min before reaching the wrapped handler.
// It exists because the OTel bridge reports every level as enabled, so
// without the gate Debug records would also export.
type MinLevel struct {
	slog.Handler
	Min slog.Level
}

func (g MinLevel) Enabled(ctx context.Context, l slog.Level) bool {
	return l >= g.Min && g.Handler.Enabled(ctx, l)
}

func (g MinLevel) WithAttrs(attrs []slog.Attr) slog.Handler {
	return MinLevel{Handler: g.Handler.WithAttrs(attrs), Min: g.Min}
}

func (g MinLevel) WithGroup(name string) slog.Handler {
	return MinLevel{Handler: g.Handler.WithGroup(name), Min: g.Min}
}

// otlpMinLevel: production exports INFO and above (INFO, WARN, ERROR),
// anything else (dev/test) exports everything from DEBUG up.
func otlpMinLevel() slog.Level {
	if strings.EqualFold(os.Getenv("APP_ENV"), "production") {
		return slog.LevelInfo
	}
	return slog.LevelDebug
}

// NewLogHandler fans out: OTel at otlpMinLevel(), stderr JSON at Debug and
// above (everything). Build it after SetupOTelSDK so the bridge captures the
// real LoggerProvider instead of the noop.
func NewLogHandler(service string) slog.Handler {
	return slog.NewMultiHandler(
		MinLevel{Handler: otelslog.NewHandler(service), Min: otlpMinLevel()},
		slog.NewJSONHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelDebug}),
	)
}
