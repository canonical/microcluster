package endpoints

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/canonical/lxd/shared"
	"github.com/canonical/lxd/shared/api"

	"github.com/canonical/microcluster/v3/internal/log"
)

// Network represents an HTTPS listener and its server.
type Network struct {
	address     api.URL
	certMu      sync.RWMutex
	cert        *shared.CertInfo
	networkType EndpointType

	listener net.Listener
	server   *http.Server

	ctx    context.Context
	cancel context.CancelFunc

	drainConnectionsTimeout time.Duration
}

// NewNetwork assigns an address, certificate, and server to the Network.
func NewNetwork(ctx context.Context, endpointType EndpointType, server *http.Server, address api.URL, cert *shared.CertInfo, drainConnTimeout time.Duration) *Network {
	ctx, cancel := context.WithCancel(ctx)

	return &Network{
		address:     address,
		cert:        cert,
		networkType: endpointType,

		server: server,
		ctx:    ctx,
		cancel: cancel,

		drainConnectionsTimeout: drainConnTimeout,
	}
}

// log is a convenience to retrieve the internal logger from the network's context.
// We always expect the logger to be present.
func (n *Network) log() *slog.Logger {
	return n.ctx.Value(log.CtxLogger).(*slog.Logger) //nolint:revive
}

// Type returns the type of the Endpoint.
func (n *Network) Type() EndpointType {
	return n.networkType
}

// Listen on the given address.
func (n *Network) Listen() error {
	listenAddress := canonicalNetworkAddress(n.address.URL.Host, shared.HTTPSDefaultPort)
	protocol := "tcp"

	if strings.HasPrefix(listenAddress, "0.0.0.0") {
		protocol = "tcp4"
	}

	_, err := net.Dial(protocol, listenAddress)
	if err == nil {
		return fmt.Errorf("%q listener with address %q is already running", protocol, listenAddress)
	}

	listener, err := net.Listen(protocol, listenAddress)
	if err != nil {
		return fmt.Errorf("Failed to listen on https socket: %w", err)
	}

	// Use the mutableTLSListener that wraps connections at Accept time
	n.listener = newMutableTLSListener(listener, n.cert)

	return nil
}

// UpdateTLS updates the TLS configuration of the network listener.
func (n *Network) UpdateTLS(cert *shared.CertInfo) {
	l, ok := n.listener.(*mutableTLSListener)
	if ok {
		n.certMu.Lock()
		n.cert = cert
		n.certMu.Unlock()

		l.Config(cert)
	}
}

// TLS returns the network's certificate information.
func (n *Network) TLS() *shared.CertInfo {
	n.certMu.RLock()
	defer n.certMu.RUnlock()

	certCopy := *n.cert
	return &certCopy
}

// Serve binds to the Network's server.
func (n *Network) Serve() {
	if n.listener == nil {
		return
	}

	n.log().Info("Binding https socket", slog.String("network", n.listener.Addr().String()))

	go func() {
		select {
		case <-n.ctx.Done():
			n.log().Info("Received shutdown signal - aborting https socket server startup")
		default:
			// server.Serve always returns a non-nil error.
			// http.ErrServerClosed is returned after server.Shutdown or server.Close.
			// net.ErrClosed is returned if the listener is closed.
			err := n.server.Serve(n.listener)
			if errors.Is(err, http.ErrServerClosed) || errors.Is(err, net.ErrClosed) {
				n.log().Info("Received shutdown signal - stopped serving https socket listener")
			} else {
				n.log().Error("Failed to start server", slog.String("error", err.Error()))
			}
		}
	}()
}

// Close the listener.
func (n *Network) Close() error {
	if n.listener == nil {
		return nil
	}

	n.log().Info("Stopping REST API handler - closing https socket", slog.String("address", n.listener.Addr().String()))
	defer n.cancel()

	// n.listener.Close() will mean that we'll no longer accept connections.
	// It does not shutdown the server, or its currently accepted connections.
	// We need to shut this down separately, as the listener is not passed to the server
	// if n.Serve() is not called.
	return n.listener.Close()
}

// Shutdown will attempt to close the server gracefully, if configured with a drain connections timeout.
// Note that graceful shutdown will timeout if the connections do not finish (e.g.: a request caused the server
// to Close the endpoints on the same goroutine).
func (n *Network) Shutdown() error {
	return shutdownServer(n.ctx, n.server, n.drainConnectionsTimeout)
}
