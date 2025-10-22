package endpoints

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"time"

	"github.com/canonical/lxd/shared/logger"
)

// shutdownServer will shutdown the given server.
// If the given timeout is 0, it will forcefully shut it down. Otherwise, it will gracefully shut it down.
func shutdownServer(server *http.Server, timeout time.Duration) error {
	// If the given timeout is 0, force the shutdown.
	if timeout == 0 {
		err := server.Close()
		if errors.Is(err, net.ErrClosed) {
			return nil
		}

		return err
	}

	// server.Shutdown will gracefully stop the server, allowing existing requests to finish.
	shutdownCtx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	err := server.Shutdown(shutdownCtx)
	if err != nil {
		logger.Error("Failed to gracefully shutdown server", logger.Ctx{"err": err})
		closeErr := server.Close()
		if closeErr != nil {
			logger.Error("Failed to close server", logger.Ctx{"err": closeErr})
			return fmt.Errorf("Encountered error while closing server: %w, after failing to gracefully shutdown the server: %w", closeErr, err)
		}

		return err
	}

	return nil
}
