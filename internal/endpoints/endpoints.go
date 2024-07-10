package endpoints

import (
	"context"
	"sync"

	"github.com/canonical/lxd/shared"
	"github.com/canonical/lxd/shared/logger"
)

// Endpoints represents all listeners and servers for the microcluster daemon REST API.
type Endpoints struct {
	mu          sync.RWMutex
	shutdownCtx context.Context // Parent context for shutting down cleanly.

	listeners map[string]Endpoint // Map of supported listeners.
}

// NewEndpoints aggregates the given endpoints so we can manage them from one source.
func NewEndpoints(shutdownCtx context.Context, endpoints map[string]Endpoint) *Endpoints {
	return &Endpoints{listeners: endpoints, shutdownCtx: shutdownCtx}
}

// Up calls Serve on each of the configured listeners.
func (e *Endpoints) Up() error {
	err := e.up(e.listeners)
	if err != nil {
		// Attempt to call Down() in case something actually got brought up.
		_ = e.Down()

		return err
	}

	return nil
}

// UpdateTLSByName updates the TLS configuration of the network listeners.
// In case the cluster certificate gets updated reload the core.
func (e *Endpoints) UpdateTLSByName(name string, cert *shared.CertInfo) {
	e.mu.Lock()
	defer e.mu.Unlock()

	endpoint, ok := e.listeners[name]
	if ok {
		network, ok := endpoint.(*Network)
		if ok {
			network.UpdateTLS(cert)
		}
	}
}

// Add calls Serve on the additional set of listeners, and adds them to Endpoints.
func (e *Endpoints) Add(endpoints map[string]Endpoint) error {
	for k, v := range endpoints {
		e.listeners[k] = v
	}

	err := e.up(endpoints)
	if err != nil {
		// Attempt to call DownByName() in case something actually got brought up.
		for name := range endpoints {
			_ = e.DownByName(name)
		}

		return err
	}

	return nil
}

func (e *Endpoints) up(listeners map[string]Endpoint) error {
	e.mu.Lock()
	defer e.mu.Unlock()

	// Startup listeners.
	for _, listener := range listeners {
		listenerCopy := listener

		err := listenerCopy.Listen()
		if err != nil {
			return err
		}

		go func() {
			select {
			case <-e.shutdownCtx.Done():
				logger.Infof("Received shutdown signal - aborting endpoint startup for %s", listenerCopy.Type().String())
				return

			default:
				listenerCopy.Serve()
			}
		}()
	}

	return nil
}

// Down closes all of the configured listeners, or any for the type specifically supplied.
func (e *Endpoints) Down(types ...EndpointType) error {
	e.mu.Lock()
	defer e.mu.Unlock()

	for name, endpoint := range e.listeners {
		remove := false
		for _, endpointType := range types {
			if endpoint.Type() == endpointType {
				remove = true
			}
		}

		if types == nil || remove {
			err := endpoint.Close()
			if err != nil {
				return err
			}

			// Delete the stopped endpoint from the slice.
			delete(e.listeners, name)
		}
	}

	return nil
}

// DownByName closes the configured listeners based on its name.
func (e *Endpoints) DownByName(name string) error {
	e.mu.Lock()
	defer e.mu.Unlock()

	for endpointName, endpoint := range e.listeners {
		if endpointName == name {
			err := endpoint.Close()
			if err != nil {
				return err
			}

			// Delete the stopped endpoint from the slice.
			delete(e.listeners, name)
		}
	}

	return nil
}

// List returns a list of already added endpoints.
func (e *Endpoints) List(types ...EndpointType) map[string]Endpoint {
	e.mu.Lock()
	defer e.mu.Unlock()

	var endpoints = make(map[string]Endpoint, 0)
	for name, endpoint := range e.listeners {
		if shared.ValueInSlice(endpoint.Type(), types) {
			endpoints[name] = endpoint
		}
	}

	return endpoints
}
