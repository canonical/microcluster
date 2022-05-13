package endpoints

import (
	"context"
	"sync"

	"github.com/canonical/microcluster/internal/logger"
)

// Endpoints represents all listeners and servers for the microcluster daemon REST API.
type Endpoints struct {
	mu          sync.RWMutex
	shutdownCtx context.Context // Parent context for shutting down cleanly.

	listeners map[EndpointType]Endpoint // Map of supported listeners.
}

// NewEndpoints aggregates the given endpoints so we can manage them from one source.
func NewEndpoints(shutdownCtx context.Context, endpoints ...Endpoint) *Endpoints {
	listeners := map[EndpointType]Endpoint{}
	for _, endpoint := range endpoints {
		listeners[endpoint.Type()] = endpoint
	}

	return &Endpoints{listeners: listeners, shutdownCtx: shutdownCtx}
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

// Add calls Serve on the additional set of listeners, and adds them to Endpoints.
func (e *Endpoints) Add(endpoints ...Endpoint) error {
	newListeners := map[EndpointType]Endpoint{}
	for _, endpoint := range endpoints {
		newListeners[endpoint.Type()] = endpoint
	}

	for k, v := range newListeners {
		e.listeners[k] = v
	}

	err := e.up(newListeners)
	if err != nil {
		// Attempt to call Down() in case something actually got brought up.
		_ = e.Down()

		return err
	}

	return nil
}

func (e *Endpoints) up(listeners map[EndpointType]Endpoint) error {
	e.mu.Lock()
	defer e.mu.Unlock()

	// Startup listeners.
	for key := range listeners {
		err := listeners[key].Listen()
		if err != nil {
			return err
		}

		listener := listeners[key]

		go func() {
			select {
			case <-e.shutdownCtx.Done():
				logger.Infof("Received shutdown signal - aborting endpoint startup for %s", listener.Type().String())
				return

			default:
				listener.Serve()
			}
		}()
	}

	return nil
}

// Down closes all of the configured listeners.
func (e *Endpoints) Down() error {
	e.mu.Lock()
	defer e.mu.Unlock()

	for _, listener := range e.listeners {
		err := listener.Close()
		if err != nil {
			return err
		}
	}

	return nil
}
