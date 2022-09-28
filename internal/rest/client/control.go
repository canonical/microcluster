package client

import (
	"context"
	"time"

	"github.com/canonical/microcluster/internal/rest/types"
)

// ControlDaemon posts control data to the daemon.
func (c *Client) ControlDaemon(ctx context.Context, args types.Control, timeout time.Duration) error {
	if timeout == 0 {
		return c.QueryStruct(ctx, "POST", ControlEndpoint, nil, args, nil)
	}

	queryCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	return c.QueryStruct(queryCtx, "POST", ControlEndpoint, nil, args, nil)
}
