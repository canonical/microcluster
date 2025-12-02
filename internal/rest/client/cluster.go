package client

import (
	"context"
	"time"

	"github.com/canonical/lxd/shared/api"

	"github.com/canonical/microcluster/v3/microcluster/types"
)

// withTimeoutIfUnset returns a context with a 30s timeout only if the parent context has no deadline set.
func withTimeoutIfUnset(ctx context.Context) (context.Context, context.CancelFunc) {
	_, ok := ctx.Deadline()
	if ok {
		return ctx, func() {}
	}

	return context.WithTimeout(ctx, 30*time.Second)
}

// AddClusterMember records a new cluster member in the trust store of each current cluster member.
func AddClusterMember(ctx context.Context, c *Client, args types.ClusterMember) (*types.TokenResponse, error) {
	queryCtx, cancel := withTimeoutIfUnset(ctx)
	defer cancel()

	tokenResponse := types.TokenResponse{}
	err := c.QueryStruct(queryCtx, "POST", types.InternalEndpoint, &api.NewURL().Path("cluster").URL, args, &tokenResponse)
	if err != nil {
		return nil, err
	}

	return &tokenResponse, nil
}

// ResetClusterMember clears the state directory of the cluster member, and re-execs its daemon.
func ResetClusterMember(ctx context.Context, c *Client, name string, force bool) error {
	queryCtx, cancel := withTimeoutIfUnset(ctx)
	defer cancel()

	endpoint := api.NewURL().Path("cluster", name)
	if force {
		endpoint = endpoint.WithQuery("force", "1")
	}

	return c.QueryStruct(queryCtx, "PUT", types.InternalEndpoint, &endpoint.URL, nil, nil)
}

// GetClusterMembers returns the database record of cluster members.
func (c *Client) GetClusterMembers(ctx context.Context) ([]types.ClusterMember, error) {
	queryCtx, cancel := withTimeoutIfUnset(ctx)
	defer cancel()

	clusterMembers := []types.ClusterMember{}
	err := c.QueryStruct(queryCtx, "GET", types.PublicEndpoint, &api.NewURL().Path("cluster").URL, nil, &clusterMembers)

	return clusterMembers, err
}

// DeleteClusterMember deletes the cluster member with the given name.
func (c *Client) DeleteClusterMember(ctx context.Context, name string, force bool) error {
	queryCtx, cancel := withTimeoutIfUnset(ctx)
	defer cancel()

	endpoint := api.NewURL().Path("cluster", name)
	if force {
		endpoint = endpoint.WithQuery("force", "1")
	}

	return c.QueryStruct(queryCtx, "DELETE", types.PublicEndpoint, &endpoint.URL, nil, nil)
}

// UpdateCertificate sets a new keypair and CA.
func (c *Client) UpdateCertificate(ctx context.Context, name types.CertificateName, args types.KeyPair) error {
	queryCtx, cancel := withTimeoutIfUnset(ctx)
	defer cancel()

	endpoint := api.NewURL().Path("cluster", "certificates", string(name))
	return c.QueryStruct(queryCtx, "PUT", types.PublicEndpoint, &endpoint.URL, args, nil)
}
