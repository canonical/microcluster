package client

import (
	"context"
	"net/http"

	clusterRequest "github.com/canonical/lxd/lxd/cluster/request"
	"github.com/canonical/lxd/shared/api"

	"github.com/canonical/microcluster/v3/internal/rest/client"
	"github.com/canonical/microcluster/v3/rest/types"
)

// Client is a rest client for the MicroCluster daemon.
type Client struct {
	client.Client
}

// IsNotification determines if this request is to be considered a cluster-wide notification.
func IsNotification(r *http.Request) bool {
	return r.Header.Get("User-Agent") == clusterRequest.UserAgentNotifier
}

// Query is a helper for initiating a request on any endpoints defined external to Microcluster. This function should be used for all client
// methods defined externally from MicroCluster.
func (c *Client) Query(ctx context.Context, method string, prefix types.EndpointPrefix, path *api.URL, in any, out any) error {
	queryCtx, cancel := context.WithCancel(ctx)
	defer cancel()

	return c.QueryStruct(queryCtx, method, prefix, path, in, &out)
}

// UseTarget returns a new client with the query "?target=name" set.
func (c *Client) UseTarget(name string) *Client {
	newClient := c.Client.UseTarget(name)

	return &Client{Client: *newClient}
}
