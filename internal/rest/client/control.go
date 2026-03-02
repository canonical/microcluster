package client

import (
	"context"

	"github.com/canonical/microcluster/v4/microcluster/types"
)

// ControlDaemon posts control data to the daemon.
func ControlDaemon(ctx context.Context, c types.Client, args types.Control) error {
	return c.Query(ctx, "POST", types.ControlEndpoint, nil, args, nil)
}
