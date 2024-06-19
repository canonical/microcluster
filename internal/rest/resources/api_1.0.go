package resources

import (
	"net/http"

	"github.com/canonical/lxd/lxd/response"

	internalTypes "github.com/canonical/microcluster/internal/rest/types"
	"github.com/canonical/microcluster/rest"
	"github.com/canonical/microcluster/rest/types"
	"github.com/canonical/microcluster/state"
)

var api10Cmd = rest.Endpoint{
	AllowedBeforeInit: true,

	Get: rest.EndpointAction{Handler: api10Get, AllowUntrusted: true},
}

func api10Get(s state.State, r *http.Request) response.Response {
	addrPort, err := types.ParseAddrPort(s.Address().URL.Host)
	if err != nil {
		return response.SmartError(err)
	}

	return response.SyncResponse(true, internalTypes.Server{
		Name:    s.Name(),
		Address: addrPort,
		Ready:   s.Database().IsOpen(),
	})
}
