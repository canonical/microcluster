package types

import (
	"context"
	"crypto/x509"
	"fmt"
	"math/rand"
	"net/http"
	"net/url"

	"github.com/gorilla/websocket"
	"golang.org/x/sync/errgroup"
)

// Client represents a client allowing to communicate with a specific cluster member.
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

// Clients represents a list of clients allowing to communicate with multiple cluster members.
type Clients []Client

// Connector represents various entry points to communicate with specific or multiple cluster members.
type Connector interface {
	// Cluster returns a client to every cluster member according to dqlite.
	Cluster(isNotification bool) (Clients, error)

	// Leader returns a client to the dqlite cluster leader.
	Leader(isNotification bool) (Client, error)

	// Member returns a client to the specified member.
	Member(url *url.URL, isNotification bool, cert *x509.Certificate) (Client, error)
}

// SelectRandom returns a randomly selected client.
func (c Clients) SelectRandom() (*Client, error) {
	switch len(c) {
	case 0:
		// Returns an error if the cluster is uninitialized (not bootstrapped, not joined).
		return nil, fmt.Errorf("Cluster is uninitialized or has no members")
	case 1:
		// Returns the only available client if cluster size is 1.
		return &c[0], nil
	default:
		// Returns a randomly selected client for clusters with multiple members.
		return &c[rand.Intn(len(c))], nil
	}
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

	g, ctx := errgroup.WithContext(ctx)

	for _, client := range c {
		g.Go(func() error {
			return query(ctx, client)
		})
	}

	// Wait for all queries to complete and check for any errors.
	// The first observed error will be returned.
	return g.Wait()
}
