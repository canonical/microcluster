package resources

import (
	"testing"

	"github.com/canonical/microcluster/v3/rest"
)

var validServers = map[string]rest.Server{
	"coreConsumer": {
		CoreAPI: true,
		Resources: []rest.Resources{
			{
				PathPrefix: "core_consumer",
				Endpoints: []rest.Endpoint{
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

var invalidServers = map[string]rest.Server{
	"emptyResources": {
		CoreAPI: true,
	},
	"emptyEndpoints": {
		CoreAPI:   true,
		Resources: []rest.Resources{},
	},
	"duplicate": {
		Resources: []rest.Resources{
			{
				PathPrefix: "dup",
				Endpoints: []rest.Endpoint{
					{
						Path: "duplicate",
					},
				},
			},
			{
				PathPrefix: "dup",
				Endpoints: []rest.Endpoint{
					{
						Path: "duplicate",
					},
				},
			},
		},
	},
	"overlapCore": {
		CoreAPI: true,
		Resources: []rest.Resources{
			{
				PathPrefix: "core",
				Endpoints: []rest.Endpoint{
					{
						Path: "hello",
					},
				},
			},
		},
	},
	"overlapCoreMultipart": {
		CoreAPI: true,
		Resources: []rest.Resources{
			{
				PathPrefix: "core/subpoint",
				Endpoints: []rest.Endpoint{
					{
						Path: "hello",
					},
				},
			},
		},
	},
	"overlapCoreEndpoint": {
		CoreAPI: true,
		Resources: []rest.Resources{
			{
				Endpoints: []rest.Endpoint{
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
		servers := map[string]rest.Server{
			serverName: server,
		}
		err := ValidateEndpoints(servers, "localhost:8000")
		if err == nil {
			t.Errorf("Invalid server %q passed validation", serverName)
		}
	}
}
