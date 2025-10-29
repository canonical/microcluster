package endpoints

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"time"

	"github.com/canonical/microcluster/v3/internal/log"
)

// shutdownServer will shutdown the given server.
// If the given timeout is 0, it will forcefully shut it down. Otherwise, it will gracefully shut it down.
func shutdownServer(ctx context.Context, server *http.Server, timeout time.Duration) error {
	// If the given timeout is 0, force the shutdown.
	if timeout == 0 {
		err := server.Close()
		if errors.Is(err, net.ErrClosed) {
			return nil
		}

		return err
	}

	// server.Shutdown will gracefully stop the server, allowing existing requests to finish.
	shutdownCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	err := server.Shutdown(shutdownCtx)
	if err != nil {
		logger, logErr := log.LoggerFromContext(shutdownCtx)
		if logErr != nil {
			return logErr
		}

		logger.Error("Failed to gracefully shutdown server", slog.String("error", err.Error()))
		closeErr := server.Close()
		if closeErr != nil {
			logger.Error("Failed to close server", slog.String("error", closeErr.Error()))

			return fmt.Errorf("Encountered error while closing server: %w, after failing to gracefully shutdown the server: %w", closeErr, err)
		}

		return err
	}

	return nil
}
