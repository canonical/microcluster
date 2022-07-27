package api

import (
	"github.com/canonical/microcluster/rest"
)

// Endpoints is a global list of all API endpoints on the /public endpoint of
// microcluster, as supplied by this example project.
var Endpoints = []rest.Endpoint{
	extendedCmd,
}
