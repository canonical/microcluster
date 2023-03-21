package client

import (
	"context"
	"time"

	"github.com/lxc/lxd/shared/api"
)

// CheckReady returns once the daemon has signalled to the ready channel that it is done setting up.
func (c *Client) CheckReady(ctx context.Context) error {
	queryCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()

	err := c.QueryStruct(queryCtx, "GET", PublicEndpoint, api.NewURL().Path("ready"), nil, nil)

	return err
}
