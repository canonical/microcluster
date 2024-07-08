package client

import (
	"context"
	"time"

	"github.com/canonical/lxd/shared/api"

	"github.com/canonical/microcluster/internal/rest/types"
	"github.com/canonical/microcluster/rest/types"
)

// RunPreRemoveHook executes the PreRemove hook with the given configuration on the cluster member targeted by this client.
func RunPreRemoveHook(ctx context.Context, c *Client, config types.HookRemoveMemberOptions) error {
	queryCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()

	return c.QueryStruct(queryCtx, "POST", types.InternalEndpoint, api.NewURL().Path("hooks", string(types.PreRemove)), config, nil)
}

// RunPostRemoveHook executes the PostRemove hook with the given configuration on the cluster member targeted by this client.
func RunPostRemoveHook(ctx context.Context, c *Client, config types.HookRemoveMemberOptions) error {
	queryCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()

	return c.QueryStruct(queryCtx, "POST", types.InternalEndpoint, api.NewURL().Path("hooks", string(types.PostRemove)), config, nil)
}

// RunNewMemberHook executes the OnNewMember hook with the given configuration on the cluster member targeted by this client.
func RunNewMemberHook(ctx context.Context, c *Client, config types.HookNewMemberOptions) error {
	queryCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()

	return c.QueryStruct(queryCtx, "POST", types.InternalEndpoint, api.NewURL().Path("hooks", string(types.OnNewMember)), config, nil)
}

// RunOnDaemonConfigUpdateHook executes the OnDaemonConfigUpdate hook with the given configuration on the cluster member targeted by this client.
func RunOnDaemonConfigUpdateHook(ctx context.Context, c *Client, config *types.DaemonConfig) error {
	queryCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()

	return c.QueryStruct(queryCtx, "POST", types.InternalEndpoint, api.NewURL().Path("hooks", string(types.OnDaemonConfigUpdate)), config, nil)
}
