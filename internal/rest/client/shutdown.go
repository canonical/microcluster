package client

import (
	"context"
	"time"

	"github.com/lxc/lxd/shared/api"
)

// ShutdownDaemon begins the daemon shutdown sequence.
func (c *Client) ShutdownDaemon(ctx context.Context) error {
	queryCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()

	return c.QueryStruct(queryCtx, "POST", ControlEndpoint, api.NewURL().Path("shutdown"), nil, nil)
}
