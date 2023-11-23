package types

import (
	"github.com/canonical/microcluster/rest/types"
)

// Control represents the arguments that can be used to initialize/shutdown the daemon.
type Control struct {
	Bootstrap  bool              `json:"bootstrap" yaml:"bootstrap"`
	InitConfig map[string]string `json:"config" yaml:"config"`
	JoinToken  string            `json:"join_token" yaml:"join_token"`
	Address    types.AddrPort    `json:"address" yaml:"address"`
	Name       string            `json:"name" yaml:"name"`
}
