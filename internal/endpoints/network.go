package endpoints

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"strings"

	"github.com/lxc/lxd/lxd/endpoints/listeners"
	"github.com/lxc/lxd/lxd/util"
	"github.com/lxc/lxd/shared"
	"github.com/lxc/lxd/shared/api"
	"github.com/lxc/lxd/shared/logger"
)

// Network represents an HTTPS listener and its server.
type Network struct {
	address     api.URL
	cert        *shared.CertInfo
	networkType EndpointType

	listener net.Listener
	server   *http.Server

	ctx context.Context
}

// NewNetwork assigns an address, certificate, and server to the Network.
func NewNetwork(ctx context.Context, endpointType EndpointType, server *http.Server, address api.URL, cert *shared.CertInfo) *Network {
	return &Network{
		address:     address,
		cert:        cert,
		networkType: endpointType,

		server: server,
		ctx:    ctx,
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

	return n.listener.Close()
}
