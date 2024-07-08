package resources

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"

	"github.com/canonical/lxd/lxd/response"
	"github.com/gorilla/mux"

	"github.com/canonical/microcluster/internal/rest/types"
	"github.com/canonical/microcluster/internal/state"
	"github.com/canonical/microcluster/rest"
	"github.com/canonical/microcluster/rest/access"
	"github.com/canonical/microcluster/rest/types"
)

var hooksCmd = rest.Endpoint{
	Path: "hooks/{hookType}",

	Post: rest.EndpointAction{Handler: hooksPost, AccessHandler: access.AllowAuthenticated, ProxyTarget: true},
}

func hooksPost(s *state.State, r *http.Request) response.Response {
	hookTypeStr, err := url.PathUnescape(mux.Vars(r)["hookType"])
	if err != nil {
		return response.SmartError(err)
	}

	switch types.HookType(hookTypeStr) {
	case types.PreRemove:
		var req types.HookRemoveMemberOptions
		err = json.NewDecoder(r.Body).Decode(&req)
		if err != nil {
			return response.BadRequest(err)
		}

		err = state.PreRemoveHook(s, req.Force)
		if err != nil {
			return response.SmartError(fmt.Errorf("Failed to execute pre-remove hook on cluster member %q: %w", s.Name(), err))
		}
	case types.PostRemove:
		var req types.HookRemoveMemberOptions
		err = json.NewDecoder(r.Body).Decode(&req)
		if err != nil {
			return response.BadRequest(err)
		}

		err = state.PostRemoveHook(s, req.Force)
		if err != nil {
			return response.SmartError(fmt.Errorf("Failed to execute post-remove hook on cluster member %q: %w", s.Name(), err))
		}

	case types.OnNewMember:
		var req types.HookNewMemberOptions
		err = json.NewDecoder(r.Body).Decode(&req)
		if err != nil {
			return response.BadRequest(err)
		}

		if req.Name == "" {
			return response.SmartError(fmt.Errorf("No new member name given for NewMember hook execution"))
		}

		err = state.OnNewMemberHook(s)
		if err != nil {
			return response.SmartError(fmt.Errorf("Failed to run hook after system %q has joined the cluster: %w", req.Name, err))
		}
	case internalTypes.OnDaemonConfigUpdate:
		var req types.DaemonConfig
		err = json.NewDecoder(r.Body).Decode(&req)
		if err != nil {
			return response.BadRequest(err)
		}

		err = state.OnDaemonConfigUpdate(s, req)
		if err != nil {
			return response.SmartError(fmt.Errorf("Failed to run hook on %q after daemon received local config update: %w", s.Name(), err))
		}
	default:
		return response.SmartError(fmt.Errorf("No valid hook found for the given type"))
	}

	return response.EmptySyncResponse
}
