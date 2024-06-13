package cluster

import (
	"fmt"

	"github.com/canonical/go-dqlite"
	dqliteClient "github.com/canonical/go-dqlite/client"
)

// DqliteMember is the information that can be derived locally about a cluster
// member without access to the dqlite database.
type DqliteMember struct {
	// dqlite.NodeInfo fields
	DqliteID uint64 `json:"id" yaml:"id"`
	Address  string `json:"address" yaml:"address"`
	Role     string `json:"role" yaml:"role"`

	Name string `json:"name" yaml:"name"`
}

// NodeInfo is used for interop with go-dqlite.
func (m DqliteMember) NodeInfo() (*dqlite.NodeInfo, error) {
	var role dqliteClient.NodeRole
	switch m.Role {
	case "voter":
		role = dqliteClient.Voter
	case "stand-by":
		role = dqliteClient.StandBy
	case "spare":
		role = dqliteClient.Spare
	default:
		return nil, fmt.Errorf("invalid dqlite role %q", m.Role)
	}

	return &dqlite.NodeInfo{
		ID:      m.DqliteID,
		Role:    role,
		Address: m.Address,
	}, nil
}
