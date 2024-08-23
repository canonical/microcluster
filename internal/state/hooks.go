package state

import (
	"context"

	"github.com/canonical/microcluster/v3/rest/types"
)

// Hooks holds customizable functions that can be called at varying points by the daemon to.
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
	OnHeartbeat func(ctx context.Context, s State, roleStatus map[string]types.RoleStatus) error

	// OnNewMember is run on each peer after a new cluster member has joined and executed their 'PreJoin' hook.
	OnNewMember func(ctx context.Context, s State, newMember types.ClusterMemberLocal) error

	// OnDaemonConfigUpdate is a post-action hook that is run on all cluster members when any cluster member receives a local configuration update.
	OnDaemonConfigUpdate func(ctx context.Context, s State, config types.DaemonConfig) error
}
