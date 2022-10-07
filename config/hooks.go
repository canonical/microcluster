package config

import (
	"github.com/canonical/microcluster/internal/state"
)

// Hooks holds customizable functions that can be called at varying points by the daemon to.
// integrate with other tools.
type Hooks struct {
	// OnBootstrap is run after the daemon is initialized and bootstrapped.
	OnBootstrap func(s *state.State) error

	// OnStart is run after the daemon is started.
	OnStart func(s *state.State) error

	// OnJoin is run after the daemon is initialized and joins a cluster.
	OnJoin func(s *state.State) error

	// PreRemove is run on a cluster member just before it is removed from the cluster.
	PreRemove func(s *state.State) error

	// PostRemove is run on all other peers after one is removed from the cluster.
	PostRemove func(s *state.State) error

	// OnHeartbeat is run after a successful heartbeat round.
	OnHeartbeat func(s *state.State) error

	// OnNewMember is run on each peer after a new cluster member has joined.
	OnNewMember func(s *state.State) error
}
