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

// ResetClusterMember clears the state directory of the cluster member, and re-execs its daemon.
func ResetClusterMember(ctx context.Context, c types.Client, name string, force bool) error {
	queryCtx, cancel := withTimeoutIfUnset(ctx)
	defer cancel()

	endpoint := api.NewURL().Path("cluster", name)
	if force {
		endpoint = endpoint.WithQuery("force", "1")
	}

	return c.Query(queryCtx, "PUT", types.InternalEndpoint, &endpoint.URL, nil, nil)
}

// AddClusterMember records a new cluster member in the trust store of each current cluster member.
func AddClusterMember(ctx context.Context, c types.Client, args types.ClusterMember) (*types.TokenResponse, error) {
	queryCtx, cancel := withTimeoutIfUnset(ctx)
	defer cancel()

	tokenResponse := types.TokenResponse{}
	err := c.Query(queryCtx, "POST", types.InternalEndpoint, &api.NewURL().Path("cluster").URL, args, &tokenResponse)
	if err != nil {
		return nil, err
	}

	return &tokenResponse, nil
}

// GetClusterMembers returns the database record of cluster members.
func GetClusterMembers(ctx context.Context, c types.Client) ([]types.ClusterMember, error) {
	queryCtx, cancel := withTimeoutIfUnset(ctx)
	defer cancel()

	clusterMembers := []types.ClusterMember{}
	err := c.Query(queryCtx, "GET", types.InternalEndpoint, &api.NewURL().Path("cluster").URL, nil, &clusterMembers)

	return clusterMembers, err
}

// DeleteClusterMember deletes the cluster member with the given name.
func DeleteClusterMember(ctx context.Context, c types.Client, name string, force bool) error {
	queryCtx, cancel := withTimeoutIfUnset(ctx)
	defer cancel()

	endpoint := api.NewURL().Path("cluster", name)
	if force {
		endpoint = endpoint.WithQuery("force", "1")
	}

	return c.Query(queryCtx, "DELETE", types.InternalEndpoint, &endpoint.URL, nil, nil)
}

// UpdateCertificate sets a new keypair and CA.
func UpdateCertificate(ctx context.Context, c types.Client, name types.CertificateName, args types.KeyPair) error {
	queryCtx, cancel := withTimeoutIfUnset(ctx)
	defer cancel()

	endpoint := api.NewURL().Path("cluster", "certificates", string(name))
	return c.Query(queryCtx, "PUT", types.InternalEndpoint, &endpoint.URL, args, nil)
}
