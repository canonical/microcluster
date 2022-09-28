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

	// OnRemove is run after the daemon is removed from a cluster.
	OnRemove func(s *state.State) error

	// OnHeartbeat is run after a successful heartbeat round.
	OnHeartbeat func(s *state.State) error
}
