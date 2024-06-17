package client

import (
	"context"
	"time"

	"github.com/canonical/lxd/shared/api"

	"github.com/canonical/microcluster/internal/rest/types"
)

// ShutdownDaemon begins the daemon shutdown sequence.
func (c *Client) ShutdownDaemon(ctx context.Context) error {
	queryCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()

	return c.QueryStruct(queryCtx, "POST", types.ControlEndpoint, api.NewURL().Path("shutdown"), nil, nil)
}
