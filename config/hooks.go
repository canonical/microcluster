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
	PreRemove func(s *state.State) error

	// PostRemove is run on all other peers after one is removed from the cluster.
	PostRemove func(s *state.State) error

	// OnHeartbeat is run after a successful heartbeat round.
	OnHeartbeat func(s *state.State) error

	// OnNewMember is run on each peer after a new cluster member has joined and executed their 'PreJoin' hook.
	OnNewMember func(s *state.State) error
}
