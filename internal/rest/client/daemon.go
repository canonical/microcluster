package client

import (
	"context"
	"time"

	"github.com/canonical/lxd/shared/api"

	"github.com/canonical/microcluster/internal/rest/types"
	apiTypes "github.com/canonical/microcluster/rest/types"
)

// UpdateServers updates the additional servers config.
func (c *Client) UpdateServers(ctx context.Context, config map[string]apiTypes.ServerConfig) error {
	queryCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()

	endpoint := api.NewURL().Path("daemon", "servers")
	return c.QueryStruct(queryCtx, "PUT", types.InternalEndpoint, endpoint, config, nil)
}
