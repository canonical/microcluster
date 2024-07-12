package resources

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"

	"github.com/canonical/lxd/lxd/response"
	"github.com/gorilla/mux"

	internalTypes "github.com/canonical/microcluster/internal/rest/types"
	internalState "github.com/canonical/microcluster/internal/state"
	"github.com/canonical/microcluster/rest"
	"github.com/canonical/microcluster/rest/access"
	"github.com/canonical/microcluster/rest/types"
	"github.com/canonical/microcluster/state"
)

var hooksCmd = rest.Endpoint{
	Path: "hooks/{hookType}",

	Post: rest.EndpointAction{Handler: hooksPost, AccessHandler: access.AllowAuthenticated, ProxyTarget: true},
}

func hooksPost(s state.State, r *http.Request) response.Response {
	hookTypeStr, err := url.PathUnescape(mux.Vars(r)["hookType"])
	if err != nil {
		return response.SmartError(err)
	}

	intState, err := internalState.ToInternal(s)
	if err != nil {
		return response.SmartError(err)
	}

	switch internalTypes.HookType(hookTypeStr) {
	case internalTypes.PreRemove:
		var req internalTypes.HookRemoveMemberOptions
		err = json.NewDecoder(r.Body).Decode(&req)
		if err != nil {
			return response.BadRequest(err)
		}

		err = intState.Hooks.PreRemove(s, req.Force)
		if err != nil {
			return response.SmartError(fmt.Errorf("Failed to execute pre-remove hook on cluster member %q: %w", s.Name(), err))
		}
	case internalTypes.PostRemove:
		var req internalTypes.HookRemoveMemberOptions
		err = json.NewDecoder(r.Body).Decode(&req)
		if err != nil {
			return response.BadRequest(err)
		}

		err = intState.Hooks.PostRemove(s, req.Force)
		if err != nil {
			return response.SmartError(fmt.Errorf("Failed to execute post-remove hook on cluster member %q: %w", s.Name(), err))
		}

	case internalTypes.OnNewMember:
		var req internalTypes.HookNewMemberOptions
		err = json.NewDecoder(r.Body).Decode(&req)
		if err != nil {
			return response.BadRequest(err)
		}

		if req.Name == "" {
			return response.SmartError(fmt.Errorf("No new member name given for NewMember hook execution"))
		}

		err = intState.Hooks.OnNewMember(s)
		if err != nil {
			return response.SmartError(fmt.Errorf("Failed to run hook after system %q has joined the cluster: %w", req.Name, err))
		}
	case internalTypes.OnDaemonConfigUpdate:
		var req types.DaemonConfig
		err = json.NewDecoder(r.Body).Decode(&req)
		if err != nil {
			return response.BadRequest(err)
		}

		err = intState.Hooks.OnDaemonConfigUpdate(s, req)
		if err != nil {
			return response.SmartError(fmt.Errorf("Failed to run hook on %q after daemon received local config update: %w", s.Name(), err))
		}
	default:
		return response.SmartError(fmt.Errorf("No valid hook found for the given type"))
	}

	return response.EmptySyncResponse
}
