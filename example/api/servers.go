// Package api provides a slice of Servers
package api

import (
	apiTypes "github.com/canonical/microcluster/v3/example/api/types"
	"github.com/canonical/microcluster/v3/microcluster/types"
)

// Servers represents the list of listeners that the daemon will start
// Each Server has pre-defined endpoints that will be added to the listener
// If the Server is marked as CoreAPI, its endpoints will be added to the core listener of Microcluster.
var Servers = map[string]types.Server{
	"extended": {
		CoreAPI:   true,
		ServeUnix: true,
		Resources: []types.Resources{
			{
				PathPrefix: apiTypes.ExtendedPathPrefix,
				Endpoints: []types.Endpoint{
					extendedSimpleCmd,
					extendedWebsocketCmd,
				},
			},
		},
	},
}
