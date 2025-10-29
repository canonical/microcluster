package endpoints

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"strconv"
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

// canonicalNetworkAddress parses the given network address and returns a string of the form "host:port",
// possibly filling it with the default port if it's missing. It will also wrap a bare IPv6 address with square
// brackets if needed.
func canonicalNetworkAddress(address string, defaultPort int64) string {
	host, port, err := net.SplitHostPort(address)
	if err != nil {
		ip := net.ParseIP(address)
		if ip != nil {
			// If the input address is a bare IP address, then convert it to a proper listen address
			// using the canonical IP with default port and wrap IPv6 addresses in square brackets.
			address = net.JoinHostPort(ip.String(), strconv.FormatInt(defaultPort, 10))
		} else {
			// Otherwise assume this is either a host name or a partial address (e.g `[::]`) without
			// a port number, so append the default port.
			address = address + ":" + strconv.FormatInt(defaultPort, 10)
		}
	} else if port == "" && address[len(address)-1] == ':' {
		// An address that ends with a trailing colon will be parsed as having an empty port.
		address = net.JoinHostPort(host, strconv.FormatInt(defaultPort, 10))
	}

	return address
}
