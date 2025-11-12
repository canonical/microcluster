package resources

import (
	"net/http"

	internalState "github.com/canonical/microcluster/v3/internal/state"
	"github.com/canonical/microcluster/v3/microcluster/rest/response"
	"github.com/canonical/microcluster/v3/microcluster/types"
)

var api10Cmd = types.Endpoint{
	AllowedBeforeInit: true,

	Get: types.EndpointAction{Handler: api10Get, AllowUntrusted: true},
}

func api10Get(s types.State, r *http.Request) response.Response {
	addrPort, err := types.ParseAddrPort(s.Address().Host)
	if err != nil {
		return response.SmartError(err)
	}

	intState, err := internalState.ToInternal(s)
	if err != nil {
		return response.SmartError(err)
	}

	return response.SyncResponse(true, types.Status{
		Name:       s.Name(),
		Address:    addrPort,
		Version:    s.Version(),
		Ready:      s.Database().IsOpen(r.Context()) == nil,
		Extensions: intState.Extensions,
	})
}
