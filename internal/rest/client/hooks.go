package client

import (
	"context"
	"time"

	"github.com/canonical/lxd/shared/api"

	internalTypes "github.com/canonical/microcluster/v3/internal/rest/types"
	"github.com/canonical/microcluster/v3/rest/types"
)

// RunPreRemoveHook executes the PreRemove hook with the given configuration on the cluster member targeted by this client.
func RunPreRemoveHook(ctx context.Context, c *Client, config internalTypes.HookRemoveMemberOptions) error {
	queryCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()

	return c.QueryStruct(queryCtx, "POST", internalTypes.InternalEndpoint, &api.NewURL().Path("hooks", string(internalTypes.PreRemove)).URL, config, nil)
}

// RunPostRemoveHook executes the PostRemove hook with the given configuration on the cluster member targeted by this client.
func RunPostRemoveHook(ctx context.Context, c *Client, config internalTypes.HookRemoveMemberOptions) error {
	queryCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()

	return c.QueryStruct(queryCtx, "POST", internalTypes.InternalEndpoint, &api.NewURL().Path("hooks", string(internalTypes.PostRemove)).URL, config, nil)
}

// RunNewMemberHook executes the OnNewMember hook with the given configuration on the cluster member targeted by this client.
func RunNewMemberHook(ctx context.Context, c *Client, config internalTypes.HookNewMemberOptions) error {
	queryCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()

	return c.QueryStruct(queryCtx, "POST", internalTypes.InternalEndpoint, &api.NewURL().Path("hooks", string(internalTypes.OnNewMember)).URL, config, nil)
}

// RunOnDaemonConfigUpdateHook executes the OnDaemonConfigUpdate hook with the given configuration on the cluster member targeted by this client.
func RunOnDaemonConfigUpdateHook(ctx context.Context, c *Client, config *types.DaemonConfig) error {
	queryCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()

	return c.QueryStruct(queryCtx, "POST", internalTypes.InternalEndpoint, &api.NewURL().Path("hooks", string(internalTypes.OnDaemonConfigUpdate)).URL, config, nil)
}
