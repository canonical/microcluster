package client

import (
	"context"
	"time"

	"github.com/canonical/lxd/shared/api"

	"github.com/canonical/microcluster/v3/microcluster/types"
)

// CheckReady returns once the daemon has signalled to the ready channel that it is done setting up.
func CheckReady(ctx context.Context, c types.Client) error {
	queryCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()

	err := c.Query(queryCtx, "GET", types.PublicEndpoint, &api.NewURL().Path("ready").URL, nil, nil)

	return err
}
