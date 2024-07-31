package client

import (
	"context"
	"time"

	"github.com/canonical/lxd/shared/api"

	"github.com/canonical/microcluster/v3/internal/rest/types"
)

// CheckReady returns once the daemon has signalled to the ready channel that it is done setting up.
func (c *Client) CheckReady(ctx context.Context) error {
	queryCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()

	err := c.QueryStruct(queryCtx, "GET", types.PublicEndpoint, api.NewURL().Path("ready"), nil, nil)

	return err
}
