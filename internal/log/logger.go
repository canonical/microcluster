package log

import (
	"context"
	"errors"
	"log/slog"

	"github.com/canonical/microcluster/v3/microcluster/types"
)

// LoggerFromContext returns the logger from the given context.
func LoggerFromContext(ctx context.Context) (*slog.Logger, error) {
	logger, ok := ctx.Value(types.CtxLogger).(*slog.Logger)
	if !ok {
		return nil, errors.New("Logger does not exist on context")
	}

	return logger, nil
}
