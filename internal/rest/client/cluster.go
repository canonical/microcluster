package client

import (
	"context"
	"time"

	"github.com/canonical/lxd/shared/api"

	"github.com/canonical/microcluster/internal/rest/types"
	apiTypes "github.com/canonical/microcluster/rest/types"
)

// AddClusterMember records a new cluster member in the trust store of each current cluster member.
func (c *Client) AddClusterMember(ctx context.Context, args types.ClusterMember) (*types.TokenResponse, error) {
	queryCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()

	tokenResponse := types.TokenResponse{}
	err := c.QueryStruct(queryCtx, "POST", types.PublicEndpoint, api.NewURL().Path("cluster"), args, &tokenResponse)
	if err != nil {
		return nil, err
	}

	return &tokenResponse, nil
}

// GetClusterMembers returns the database record of cluster members.
func (c *Client) GetClusterMembers(ctx context.Context) ([]types.ClusterMember, error) {
	queryCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()

	clusterMembers := []types.ClusterMember{}
	err := c.QueryStruct(queryCtx, "GET", types.PublicEndpoint, api.NewURL().Path("cluster"), nil, &clusterMembers)

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

	return c.QueryStruct(queryCtx, "DELETE", types.PublicEndpoint, endpoint, nil, nil)
}

// ResetClusterMember clears the state directory of the cluster member, and re-execs its daemon.
func (c *Client) ResetClusterMember(ctx context.Context, name string, force bool) error {
	queryCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()

	endpoint := api.NewURL().Path("cluster", name)
	if force {
		endpoint = endpoint.WithQuery("force", "1")
	}

	return c.QueryStruct(queryCtx, "PUT", types.PublicEndpoint, endpoint, nil, nil)
}

// UpdateClusterCertificate sets a new cluster keypair and CA.
func (c *Client) UpdateClusterCertificate(ctx context.Context, name string, args apiTypes.KeyPair) error {
	queryCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()

	endpoint := api.NewURL().Path("cluster", "certificates", name)
	return c.QueryStruct(queryCtx, "PUT", types.InternalEndpoint, endpoint, args, nil)
}
