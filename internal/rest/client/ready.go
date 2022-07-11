package client

import (
	"context"
	"strings"
	"time"

	"github.com/lxc/lxd/shared/api"
)

// CheckReady returns once the daemon has signalled to the ready channel that it is done setting up.
func (c *Client) CheckReady(ctx context.Context) error {
	endpoint := PublicEndpoint
	if strings.HasSuffix(c.url.String(), "control.socket") {
		endpoint = ControlEndpoint
	}

	queryCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()

	err := c.QueryStruct(queryCtx, "GET", endpoint, api.NewURL().Path("ready"), nil, nil)

	return err
}
