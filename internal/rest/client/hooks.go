package client

import (
	"context"
	"time"

	"github.com/canonical/lxd/shared/api"

	"github.com/canonical/microcluster/v3/microcluster/types"
)

// RunPreRemoveHook executes the PreRemove hook with the given configuration on the cluster member targeted by this client.
func RunPreRemoveHook(ctx context.Context, c types.Client, config types.HookRemoveMemberOptions) error {
	queryCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()

	return c.Query(queryCtx, "POST", types.InternalEndpoint, &api.NewURL().Path("hooks", string(types.PreRemove)).URL, config, nil)
}

// RunPostRemoveHook executes the PostRemove hook with the given configuration on the cluster member targeted by this client.
func RunPostRemoveHook(ctx context.Context, c types.Client, config types.HookRemoveMemberOptions) error {
	queryCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()

	return c.Query(queryCtx, "POST", types.InternalEndpoint, &api.NewURL().Path("hooks", string(types.PostRemove)).URL, config, nil)
}

// RunNewMemberHook executes the OnNewMember hook with the given configuration on the cluster member targeted by this client.
func RunNewMemberHook(ctx context.Context, c types.Client, config types.HookNewMemberOptions) error {
	queryCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()

	return c.Query(queryCtx, "POST", types.InternalEndpoint, &api.NewURL().Path("hooks", string(types.OnNewMember)).URL, config, nil)
}

// RunOnDaemonConfigUpdateHook executes the OnDaemonConfigUpdate hook with the given configuration on the cluster member targeted by this client.
func RunOnDaemonConfigUpdateHook(ctx context.Context, c types.Client, config *types.DaemonConfig) error {
	queryCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()

	return c.Query(queryCtx, "POST", types.InternalEndpoint, &api.NewURL().Path("hooks", string(types.OnDaemonConfigUpdate)).URL, config, nil)
}
