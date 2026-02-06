package types

import "context"

// HookType represents the various types of hooks available to microcluster.
type HookType string

const (
	// OnStart is run after the daemon is started.
	OnStart HookType = "on-start"

	// PreBootstrap is run before the daemon is initialized and bootstrapped.
	PreBootstrap HookType = "pre-bootstrap"

	// PostBootstrap is run after the daemon is initialized and bootstrapped.
	PostBootstrap HookType = "post-bootstrap"

	// PreJoin is run after the daemon is initialized and joined the cluster but before existing members triggered
	// their 'OnNewMember' hooks.
	PreJoin HookType = "pre-join"

	// PostJoin is run after the daemon is initialized, joined the cluster and existing members triggered
	// their 'OnNewMember' hooks.
	PostJoin HookType = "post-join"

	// PreRemove is run on a cluster member just before it is removed from the cluster.
	PreRemove HookType = "pre-remove"

	// PostRemove is run on all other peers after one is removed from the cluster.
	PostRemove HookType = "post-remove"

	// OnNewMember is run on each peer after a new cluster member has joined and executed their 'PreJoin' hook.
	OnNewMember HookType = "on-new-member"

	// OnHeartbeat is run after a successful heartbeat round.
	OnHeartbeat HookType = "on-heartbeat"

	// OnDaemonConfigUpdate is run after the local daemon received a config update.
	OnDaemonConfigUpdate HookType = "on-daemon-config-update"
)

// HookRemoveMemberOptions holds configuration pertaining to the PreRemove and PostRemove hooks.
type HookRemoveMemberOptions struct {
	// Force represents whether to run the hook with the `force` option.
	Force bool `json:"force" yaml:"force"`
}

// HookNewMemberOptions holds configuration pertaining to the OnNewMember hook.
type HookNewMemberOptions struct {
	// Name is the name of the new cluster member that joined the cluster, triggering this hook.
	NewMember ClusterMemberLocal `json:"new_member" yaml:"new_member"`
}

// Hooks holds customizable functions that can be called at varying points by the daemon to
// integrate with other tools.
type Hooks struct {
	// PreInit is run before the daemon is initialized.
	PreInit func(ctx context.Context, s State, bootstrap bool, initConfig map[string]string) error

	// PostBootstrap is run after the daemon is initialized and bootstrapped.
	PostBootstrap func(ctx context.Context, s State, initConfig map[string]string) error

	// OnStart is run after the daemon is started. Its context will not be cancelled until the daemon is shutting down.
	OnStart func(ctx context.Context, s State) error

	// PostJoin is run after the daemon is initialized, joined the cluster and existing members triggered
	// their 'OnNewMember' hooks.
	PostJoin func(ctx context.Context, s State, initConfig map[string]string) error

	// PreJoin is run after the daemon is initialized and joined the cluster but before existing members triggered
	// their 'OnNewMember' hooks.
	PreJoin func(ctx context.Context, s State, initConfig map[string]string) error

	// PreRemove is run on a cluster member just before it is removed from the cluster.
	PreRemove func(ctx context.Context, s State, force bool) error

	// PostRemove is run on all other peers after one is removed from the cluster.
	PostRemove func(ctx context.Context, s State, force bool) error

	// OnHeartbeat is run after a successful heartbeat round.
	OnHeartbeat func(ctx context.Context, s State, roleStatus map[string]RoleStatus) error

	// OnNewMember is run on each peer after a new cluster member has joined and executed their 'PreJoin' hook.
	OnNewMember func(ctx context.Context, s State, newMember ClusterMemberLocal) error

	// OnDaemonConfigUpdate is a post-action hook that is run on all cluster members when any cluster member receives a local configuration update.
	OnDaemonConfigUpdate func(ctx context.Context, s State, config DaemonConfig) error
}
