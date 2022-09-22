package daemon

import (
	"github.com/canonical/microcluster/internal/state"
)

// defaultHooks is an empty set of hooks.
type defaultHooks struct{}

// OnBootstrapHook is run after the daemon is initialized and bootstrapped.
func (d defaultHooks) OnBootstrapHook(s *state.State) error { return nil }

// OnStartHook is run after the daemon is started.
func (d defaultHooks) OnStartHook(s *state.State) error { return nil }

// OnJoinHook is run after the daemon is initialized and joins a cluster.
func (d defaultHooks) OnJoinHook(s *state.State) error { return nil }

// OnRemoveHook is run after the daemon is removed from a cluster.
func (d defaultHooks) OnRemoveHook(s *state.State) error { return nil }

// OnHeartbeatHook is run after a successful heartbeat round.
func (d defaultHooks) OnHeartbeatHook(s *state.State) error { return nil }
