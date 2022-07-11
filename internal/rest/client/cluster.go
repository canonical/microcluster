package client

import (
	"context"
	"strings"
	"time"

	"github.com/canonical/microcluster/internal/rest/types"
	"github.com/lxc/lxd/shared/api"
)

// AddClusterMember records a new cluster member in the trust store of each current cluster member.
func (c *Client) AddClusterMember(ctx context.Context, args types.ClusterMember) error {
	queryCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()

	return c.QueryStruct(queryCtx, "POST", PublicEndpoint, api.NewURL().Path("cluster"), args, nil)
}

// GetClusterMembers returns the database record of cluster members.
func (c *Client) GetClusterMembers(ctx context.Context) ([]types.ClusterMember, error) {
	endpoint := PublicEndpoint
	if strings.HasSuffix(c.url.String(), "control.socket") {
		endpoint = ControlEndpoint
	}

	queryCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()

	clusterMembers := []types.ClusterMember{}
	err := c.QueryStruct(queryCtx, "GET", endpoint, api.NewURL().Path("cluster"), nil, &clusterMembers)

	return clusterMembers, err
}
