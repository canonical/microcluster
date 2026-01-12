package types

import (
	"context"
	"crypto/x509"
	"net/http"
	"net/url"

	"github.com/gorilla/websocket"
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
