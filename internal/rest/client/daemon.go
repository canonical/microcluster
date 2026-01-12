package client

import (
	"context"
	"time"

	"github.com/canonical/lxd/shared/api"

	"github.com/canonical/microcluster/v3/microcluster/types"
)

// UpdateServers updates the additional servers config.
func (c *Client) UpdateServers(ctx context.Context, config map[string]types.ServerConfig) error {
	queryCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()

	endpoint := api.NewURL().Path("daemon", "servers")
	return c.Query(queryCtx, "PUT", types.PublicEndpoint, &endpoint.URL, config, nil)
}
