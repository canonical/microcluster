package log

import (
	"context"
	"errors"
	"log/slog"
)

type ctxKey string

// CtxLogger is the name of the context value for the central logger.
const CtxLogger ctxKey = "logger"

// LoggerFromContext returns the logger from the given context.
func LoggerFromContext(ctx context.Context) (*slog.Logger, error) {
	logger, ok := ctx.Value(CtxLogger).(*slog.Logger)
	if !ok {
		return nil, errors.New("Logger does not exist on context")
	}

	return logger, nil
}
