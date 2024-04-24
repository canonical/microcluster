package client

import (
	"context"
	"time"

	"github.com/canonical/lxd/shared/api"
	"github.com/canonical/microcluster/internal/rest/types"
)

// AddTrustStoreEntry adds a new record to the truststore on all cluster members.
func AddTrustStoreEntry(ctx context.Context, c *Client, args types.ClusterMemberLocal) error {
	queryCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()

	return c.QueryStruct(queryCtx, "POST", InternalEndpoint, api.NewURL().Path("truststore"), args, nil)
}

// DeleteTrustStoreEntry deletes the record corresponding to the given cluster member from the trust store.
func DeleteTrustStoreEntry(ctx context.Context, c *Client, name string) error {
	queryCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()

	return c.QueryStruct(queryCtx, "DELETE", InternalEndpoint, api.NewURL().Path("truststore", name), nil, nil)
}
