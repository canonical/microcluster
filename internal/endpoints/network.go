package endpoints

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"strings"
	"sync"

	"github.com/canonical/lxd/lxd/endpoints/listeners"
	"github.com/canonical/lxd/lxd/util"
	"github.com/canonical/lxd/shared"
	"github.com/canonical/lxd/shared/api"
	"github.com/canonical/lxd/shared/logger"
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
}

// NewNetwork assigns an address, certificate, and server to the Network.
func NewNetwork(ctx context.Context, endpointType EndpointType, server *http.Server, address api.URL, cert *shared.CertInfo) *Network {
	ctx, cancel := context.WithCancel(ctx)

	return &Network{
		address:     address,
		cert:        cert,
		networkType: endpointType,

		server: server,
		ctx:    ctx,
		cancel: cancel,
	}
}

// Type returns the type of the Endpoint.
func (n *Network) Type() EndpointType {
	return n.networkType
}

// Listen on the given address.
func (n *Network) Listen() error {
	listenAddress := util.CanonicalNetworkAddress(n.address.URL.Host, shared.HTTPSDefaultPort)
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

	n.listener = listeners.NewFancyTLSListener(listener, n.cert)

	return nil
}

// UpdateTLS updates the TLS configuration of the network listener.
func (n *Network) UpdateTLS(cert *shared.CertInfo) {
	l, ok := n.listener.(*listeners.FancyTLSListener)
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

	ctx := logger.Ctx{"network": n.listener.Addr()}
	logger.Info(" - binding https socket", ctx)

	go func() {
		select {
		case <-n.ctx.Done():
			logger.Infof("Received shutdown signal - aborting https socket server startup")
		default:
			err := n.server.Serve(n.listener)
			if err != nil {
				select {
				case <-n.ctx.Done():
					logger.Infof("Received shutdown signal - aborting https socket server startup")
				default:
					logger.Error("Failed to start server", logger.Ctx{"err": err})
				}
			}
		}
	}()
}

// Close the listener.
func (n *Network) Close() error {
	if n.listener == nil {
		return nil
	}

	logger.Info("Stopping REST API handler - closing https socket", logger.Ctx{"address": n.listener.Addr()})
	n.cancel()

	return n.listener.Close()
}
