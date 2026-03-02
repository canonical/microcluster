package client

import (
	"context"
	"time"

	"github.com/canonical/lxd/shared/api"

	"github.com/canonical/microcluster/v4/microcluster/types"
)

// ShutdownDaemon begins the daemon shutdown sequence.
func ShutdownDaemon(ctx context.Context, c types.Client) error {
	queryCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()

	return c.Query(queryCtx, "POST", types.ControlEndpoint, &api.NewURL().Path("shutdown").URL, nil, nil)
}
