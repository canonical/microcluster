package config

import (
	"github.com/canonical/microcluster/internal/state"
)

// Hooks holds customizable functions that can be called at varying points by the daemon to.
// integrate with other tools.
type Hooks struct {
	// OnBootstrap is run after the daemon is initialized and bootstrapped.
	OnBootstrap func(s *state.State, initConfig map[string]string) error

	// OnStart is run after the daemon is started.
	OnStart func(s *state.State) error

	// PostJoin is run after the daemon is initialized, joined the cluster and existing members triggered
	// their 'OnNewMember' hooks.
	PostJoin func(s *state.State, initConfig map[string]string) error

	// PreJoin is run after the daemon is initialized and joined the cluster but before existing members triggered
	// their 'OnNewMember' hooks.
	PreJoin func(s *state.State, initConfig map[string]string) error

	// PreRemove is run on a cluster member just before it is removed from the cluster.
	PreRemove func(s *state.State, force bool) error

	// PostRemove is run on all other peers after one is removed from the cluster.
	PostRemove func(s *state.State, force bool) error

	// OnHeartbeat is run after a successful heartbeat round.
	OnHeartbeat func(s *state.State) error

	// OnNewMember is run on each peer after a new cluster member has joined and executed their 'PreJoin' hook.
	OnNewMember func(s *state.State) error

	// OnUpgradedMember is a hook that runs on all previously existing cluster members after a non-cluster member is upgraded to join dqlite, and runs its PreUpgrade.
	OnUpgradedMember func(s *state.State) error

	// PreUpgrade runs on a non-cluster member just before it upgrades its API and joins dqlite.
	PreUpgrade func(s *state.State, initConfig map[string]string) error

	// PostUpgrade runs on a cluster member that just upgraded from a non-cluster role, after other nodes have run the OnUpgradedMember.
	PostUpgrade func(s *state.State, initConfig map[string]string) error
}
