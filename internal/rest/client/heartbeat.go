package client

import (
	"context"
	"time"

	"github.com/canonical/microcluster/internal/rest/types"
	"github.com/lxc/lxd/shared/api"
)

// HeartbeatTimeout is the maximum request timeout for a heartbeat request.
const HeartbeatTimeout = 10

// Heartbeat initiates a new heartbeat sequence if this is a leader node.
func (c *Client) Heartbeat(ctx context.Context, hbInfo types.HeartbeatInfo) error {
	queryCtx, cancel := context.WithTimeout(ctx, HeartbeatTimeout*time.Second)
	defer cancel()

	return c.QueryStruct(queryCtx, "POST", ControlEndpoint, api.NewURL().Path("heartbeat"), hbInfo, nil)
}

// SendHeartbeat sends out a heartbeat from the leader node.
func (c *Client) SendHeartbeat(ctx context.Context, hbInfo types.HeartbeatInfo) error {
	queryCtx, cancel := context.WithTimeout(ctx, HeartbeatTimeout*time.Second)
	defer cancel()

	return c.QueryStruct(queryCtx, "POST", InternalEndpoint, api.NewURL().Path("heartbeat"), hbInfo, nil)
}
