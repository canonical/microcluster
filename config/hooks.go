package config

import (
	"github.com/canonical/microcluster/internal/state"
)

// Hooks is an interface representing customizable functions that can be called at varying points by the daemon to
// integrate with other tools.
type Hooks interface {
	// OnBootstrapHook is run after the daemon is initialized and bootstrapped.
	OnBootstrapHook(s *state.State) error

	// OnStartHook is run after the daemon is started.
	OnStartHook(s *state.State) error

	// OnJoinHook is run after the daemon is initialized and joins a cluster.
	OnJoinHook(s *state.State) error

	// OnRemoveHook is run after the daemon is removed from a cluster.
	OnRemoveHook(s *state.State) error

	// OnHeartbeatHook is run after a successful heartbeat round.
	OnHeartbeatHook(s *state.State) error
}
