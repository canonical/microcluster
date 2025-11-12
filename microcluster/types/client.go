package types

import (
	"context"
	"crypto/x509"
	"net/http"
	"net/url"
	"sync"

	"github.com/gorilla/websocket"
)

// UserAgentNotifier is the user agent used for cluster wide notifications.
// It's using the "lxd-" prefix for backwards compatibility with older cluster members
// as originally the constant from LXD's client package was used.
const UserAgentNotifier = "lxd-cluster-notifier"

type Client interface {
	// Generic functions for the client interface.
	// They allow adding custom client functions.
	URL() *url.URL
	HTTP() *http.Client
	Query(ctx context.Context, method string, prefix EndpointPrefix, path *url.URL, in any, out any) error
	QueryRaw(ctx context.Context, method string, prefix EndpointPrefix, path *url.URL, in any) (*http.Response, error)
	Websocket(ctx context.Context, prefix EndpointPrefix, path *url.URL) (*websocket.Conn, error)

	// Microcluster specific client functions.
	SetClusterNotification()
	UseTarget(name string) Client
}

type Clients []Client

type Connector interface {
	// Cluster returns a client to every cluster member according to dqlite.
	Cluster(isNotification bool) (Clients, error)

	// Leader returns a client to the dqlite cluster leader.
	Leader(isNotification bool) (Client, error)

	// Member returns a client to the specificed member.
	Member(url *url.URL, isNotification bool, cert *x509.Certificate) (Client, error)
}

// Query executes the given hook across all members of the cluster.
func (c Clients) Query(ctx context.Context, concurrent bool, query func(context.Context, Client) error) error {
	if !concurrent {
		for _, client := range c {
			err := query(ctx, client)
			if err != nil {
				return err
			}
		}

		return nil
	}

	errors := make([]error, 0, len(c))
	mut := sync.Mutex{}
	wg := sync.WaitGroup{}
	for _, client := range c {
		wg.Add(1)
		go func(client Client) {
			defer wg.Done()
			err := query(ctx, client)
			if err != nil {
				mut.Lock()
				errors = append(errors, err)
				mut.Unlock()
				return
			}
		}(client)
	}

	// Wait for all queries to complete and check for any errors.
	wg.Wait()
	for _, err := range errors {
		if err != nil {
			return err
		}
	}

	return nil
}

// IsNotification determines if this request is to be considered a cluster-wide notification.
func IsNotification(r *http.Request) bool {
	return r.Header.Get("User-Agent") == UserAgentNotifier
}
