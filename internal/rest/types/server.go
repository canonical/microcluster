package types

import (
	"github.com/canonical/microcluster/rest/types"
)

// Server represents server status information.
//
// swagger:model
type Server struct {
	// Name of the server.
	// Example: server01
	Name string `json:"name"    yaml:"name"`

	// Address of the server.
	// Example: 127.0.0.1:9000
	Address types.AddrPort `json:"address" yaml:"address"`

	// Whether the server is ready to receive requests.
	// Example: true
	Ready bool `json:"ready"   yaml:"ready"`
}
