package client

import (
	"context"
	"time"

	"github.com/canonical/lxd/shared/api"

	"github.com/canonical/microcluster/internal/rest/types"
)

// HeartbeatTimeout is the maximum request timeout for a heartbeat request.
const HeartbeatTimeout = 30

// Heartbeat initiates a new heartbeat sequence if this is a leader node.
func (c *Client) Heartbeat(ctx context.Context, hbInfo types.HeartbeatInfo) error {
	queryCtx, cancel := context.WithTimeout(ctx, HeartbeatTimeout*time.Second)
	defer cancel()

	return c.QueryStruct(queryCtx, "POST", InternalEndpoint, api.NewURL().Path("heartbeat"), hbInfo, nil)
}
