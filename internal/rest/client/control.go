package client

import (
	"context"

	"github.com/canonical/microcluster/v3/microcluster/types"
)

// ControlDaemon posts control data to the daemon.
func ControlDaemon(ctx context.Context, args types.Control, c types.Client) error {
	return c.Query(ctx, "POST", types.ControlEndpoint, nil, args, nil)
}
