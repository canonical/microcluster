package resources

import (
	"net/http"

	internalState "github.com/canonical/microcluster/v3/internal/state"
	"github.com/canonical/microcluster/v3/microcluster/types"
)

var memberCmd = types.Endpoint{
	// Allow getting some basic member status information even if not yet initialized.
	AllowedBeforeInit: true,

	Get: types.EndpointAction{Handler: memberGet, AllowUntrusted: true},
}

func memberGet(s types.State, r *http.Request) types.Response {
	addrPort, err := types.ParseAddrPort(s.Address().Host)
	if err != nil {
		return types.SmartError(err)
	}

	intState, err := internalState.ToInternal(s)
	if err != nil {
		return types.SmartError(err)
	}

	return types.SyncResponse(true, types.Status{
		Name:       s.Name(),
		Address:    addrPort,
		Version:    s.Version(),
		Ready:      s.Database().IsOpen(r.Context()) == nil,
		Extensions: intState.Extensions,
	})
}
