package client

import (
	"context"
	"time"

	"github.com/canonical/lxd/shared/api"

	internalTypes "github.com/canonical/microcluster/internal/rest/types"
	"github.com/canonical/microcluster/rest/types"
)

// AddClusterMember records a new cluster member in the trust store of each current cluster member.
func AddClusterMember(ctx context.Context, c *Client, args types.ClusterMember) (*internalTypes.TokenResponse, error) {
	queryCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()

	tokenResponse := internalTypes.TokenResponse{}
	err := c.QueryStruct(queryCtx, "POST", internalTypes.InternalEndpoint, api.NewURL().Path("cluster"), args, &tokenResponse)
	if err != nil {
		return nil, err
	}

	return &tokenResponse, nil
}

// ResetClusterMember clears the state directory of the cluster member, and re-execs its daemon.
func ResetClusterMember(ctx context.Context, c *Client, name string, force bool) error {
	queryCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()

	endpoint := api.NewURL().Path("cluster", name)
	if force {
		endpoint = endpoint.WithQuery("force", "1")
	}

	return c.QueryStruct(queryCtx, "PUT", internalTypes.InternalEndpoint, endpoint, nil, nil)
}

// GetClusterMembers returns the database record of cluster members.
func (c *Client) GetClusterMembers(ctx context.Context) ([]types.ClusterMember, error) {
	queryCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()

	clusterMembers := []types.ClusterMember{}
	err := c.QueryStruct(queryCtx, "GET", internalTypes.PublicEndpoint, api.NewURL().Path("cluster"), nil, &clusterMembers)

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

	return c.QueryStruct(queryCtx, "DELETE", internalTypes.PublicEndpoint, endpoint, nil, nil)
}

// UpdateCertificate sets a new keypair and CA.
func (c *Client) UpdateCertificate(ctx context.Context, name types.CertificateName, args types.KeyPair) error {
	queryCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()

	endpoint := api.NewURL().Path("cluster", "certificates", string(name))
	return c.QueryStruct(queryCtx, "PUT", internalTypes.PublicEndpoint, endpoint, args, nil)
}
