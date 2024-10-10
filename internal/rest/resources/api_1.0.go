package resources

import (
	"net/http"

	"github.com/canonical/lxd/lxd/response"

	internalTypes "github.com/canonical/microcluster/v3/internal/rest/types"
	internalState "github.com/canonical/microcluster/v3/internal/state"
	"github.com/canonical/microcluster/v3/rest"
	"github.com/canonical/microcluster/v3/rest/types"
	"github.com/canonical/microcluster/v3/state"
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

	intState, err := internalState.ToInternal(s)
	if err != nil {
		return response.SmartError(err)
	}

	return response.SyncResponse(true, internalTypes.Server{
		Name:       s.Name(),
		Address:    addrPort,
		Version:    s.Version(),
		Ready:      s.Database().IsOpen(r.Context()) == nil,
		Extensions: intState.Extensions,
	})
}
