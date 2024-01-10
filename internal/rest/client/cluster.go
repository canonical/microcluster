package client

import (
	"context"
	"time"

	"github.com/canonical/lxd/shared/api"
	"github.com/canonical/microcluster/internal/rest/types"
)

// UpgradeClusterMember instructs the dqlite leader to inform all nodes that the node with the given name is to be upgraded to dqlite-member.
func (c *Client) UpgradeClusterMember(ctx context.Context, args types.ClusterMemberUpgrade) error {
	queryCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()

	return c.QueryStruct(queryCtx, "PUT", PublicEndpoint, api.NewURL().Path("cluster", args.Name, "upgrade"), args, nil)
}

// AddClusterMember records a new cluster member in the trust store of each current cluster member.
func (c *Client) AddClusterMember(ctx context.Context, args types.ClusterMember, upgrading bool) (*types.TokenResponse, error) {
	queryCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()

	upgradeParam := ""
	if upgrading {
		upgradeParam = "1"
	}

	tokenResponse := types.TokenResponse{}
	err := c.QueryStruct(queryCtx, "POST", PublicEndpoint, api.NewURL().Path("cluster").WithQuery("upgrade", upgradeParam), args, &tokenResponse)
	if err != nil {
		return nil, err
	}

	return &tokenResponse, nil
}

// RegisterClusterMember instructs the dqlite leader to inform all existing cluster members to update their local records to include a newly joined system.
func (c *Client) RegisterClusterMember(ctx context.Context, args types.ClusterMember, role string, upgrading bool) error {
	queryCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()

	upgradeParam := ""
	if upgrading {
		upgradeParam = "1"
	}

	return c.QueryStruct(queryCtx, "PUT", PublicEndpoint, api.NewURL().Path("cluster").WithQuery("role", role).WithQuery("upgrade", upgradeParam), args, nil)
}

// GetClusterMembers returns the database record of cluster members.
func (c *Client) GetClusterMembers(ctx context.Context) ([]types.ClusterMember, error) {
	queryCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()

	clusterMembers := []types.ClusterMember{}
	err := c.QueryStruct(queryCtx, "GET", PublicEndpoint, api.NewURL().Path("cluster"), nil, &clusterMembers)

	return clusterMembers, err
}

// GetNonClusterMembers returns the database record of non-cluster members.
func (c *Client) GetNonClusterMembers(ctx context.Context) ([]types.ClusterMemberLocal, error) {
	queryCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()

	clusterMembers := []types.ClusterMemberLocal{}
	err := c.QueryStruct(queryCtx, "GET", PublicEndpoint, api.NewURL().Path("cluster").WithQuery("role", "non-cluster"), nil, &clusterMembers)

	return clusterMembers, err
}

// DeleteClusterMember deletes the cluster member with the given name.
func (c *Client) DeleteClusterMember(ctx context.Context, name string, force bool) error {
	queryCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()

	endpoint := api.NewURL().Path("cluster", name)
	if force {
		endpoint = endpoint.WithQuery("force", "1")
	}

	return c.QueryStruct(queryCtx, "DELETE", PublicEndpoint, endpoint, nil, nil)
}

// ResetClusterMember clears the state directory of the cluster member, and re-execs its daemon.
func (c *Client) ResetClusterMember(ctx context.Context, name string, force bool) error {
	queryCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()

	endpoint := api.NewURL().Path("cluster", name)
	if force {
		endpoint = endpoint.WithQuery("force", "1")
	}

	return c.QueryStruct(queryCtx, "PUT", PublicEndpoint, endpoint, nil, nil)
}
