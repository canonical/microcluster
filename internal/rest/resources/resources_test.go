package resources

import (
	"testing"

	"github.com/canonical/microcluster/v3/microcluster/types"
)

var validServers = map[string]types.Server{
	"coreConsumer": {
		CoreAPI: true,
		Resources: []types.Resources{
			{
				PathPrefix: "core_consumer",
				Endpoints: []types.Endpoint{
					{
						Path: "hello",
					},
				},
			},
		},
	},
}

func TestValidateEndpointsValidServers(t *testing.T) {
	err := ValidateEndpoints(validServers, "localhost:8000")
	if err != nil {
		t.Errorf("Valid server failed validation: %s", err)
	}
}

var invalidServers = map[string]types.Server{
	"emptyResources": {
		CoreAPI: true,
	},
	"emptyEndpoints": {
		CoreAPI:   true,
		Resources: []types.Resources{},
	},
	"duplicate": {
		Resources: []types.Resources{
			{
				PathPrefix: "dup",
				Endpoints: []types.Endpoint{
					{
						Path: "duplicate",
					},
				},
			},
			{
				PathPrefix: "dup",
				Endpoints: []types.Endpoint{
					{
						Path: "duplicate",
					},
				},
			},
		},
	},
	"overlapCore": {
		CoreAPI: true,
		Resources: []types.Resources{
			{
				PathPrefix: "core",
				Endpoints: []types.Endpoint{
					{
						Path: "hello",
					},
				},
			},
		},
	},
	"overlapCoreMultipart": {
		CoreAPI: true,
		Resources: []types.Resources{
			{
				PathPrefix: "core/subpoint",
				Endpoints: []types.Endpoint{
					{
						Path: "hello",
					},
				},
			},
		},
	},
	"overlapCoreEndpoint": {
		CoreAPI: true,
		Resources: []types.Resources{
			{
				Endpoints: []types.Endpoint{
					{
						Path: "core/subpoint",
					},
				},
			},
		},
	},
}

func TestValidateEndpointsInvalidServers(t *testing.T) {
	for serverName, server := range invalidServers {
		servers := map[string]types.Server{
			serverName: server,
		}

		err := ValidateEndpoints(servers, "localhost:8000")
		if err == nil {
			t.Errorf("Invalid server %q passed validation", serverName)
		}
	}
}
