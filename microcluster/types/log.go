package types

import (
	"context"
	"log/slog"
)

type CtxKey string

// CtxLogger is the name of the context value for the central logger.
const CtxLogger CtxKey = "logger"

// ContextWithLogger returns a new context with the given logger.
// If no logger is provided, the default logger is used.
func ContextWithLogger(ctx context.Context, logger ...*slog.Logger) context.Context {
	var l *slog.Logger
	if len(logger) == 0 {
		l = slog.Default()
	} else {
		l = logger[0]
	}

	return context.WithValue(ctx, CtxLogger, l)
}
