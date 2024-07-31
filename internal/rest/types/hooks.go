package types

import "github.com/canonical/microcluster/v2/rest/types"

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
	NewMember types.ClusterMemberLocal `json:"new_member" yaml:"new_member"`
}
