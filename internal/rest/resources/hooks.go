package resources

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"

	"github.com/gorilla/mux"

	"github.com/canonical/microcluster/v3/internal/rest/access"
	internalState "github.com/canonical/microcluster/v3/internal/state"
	"github.com/canonical/microcluster/v3/microcluster/types"
)

var hooksCmd = types.Endpoint{
	Path: "hooks/{hookType}",

	Post: types.EndpointAction{Handler: hooksPost, AccessHandler: access.AllowAuthenticated, ProxyTarget: true},
}

func hooksPost(s types.State, r *http.Request) types.Response {
	hookTypeStr, err := url.PathUnescape(mux.Vars(r)["hookType"])
	if err != nil {
		return types.SmartError(err)
	}

	intState, err := internalState.ToInternal(s)
	if err != nil {
		return types.SmartError(err)
	}

	ctx, cancel := context.WithCancel(r.Context())
	defer cancel()

	switch types.HookType(hookTypeStr) {
	case types.PreRemove:
		var req types.HookRemoveMemberOptions
		err = json.NewDecoder(r.Body).Decode(&req)
		if err != nil {
			return types.BadRequest(err)
		}

		err = intState.Hooks.PreRemove(ctx, s, req.Force)
		if err != nil {
			return types.SmartError(fmt.Errorf("Failed to execute pre-remove hook on cluster member %q: %w", s.Name(), err))
		}

	case types.PostRemove:
		var req types.HookRemoveMemberOptions
		err = json.NewDecoder(r.Body).Decode(&req)
		if err != nil {
			return types.BadRequest(err)
		}

		err = intState.Hooks.PostRemove(ctx, s, req.Force)
		if err != nil {
			return types.SmartError(fmt.Errorf("Failed to execute post-remove hook on cluster member %q: %w", s.Name(), err))
		}

	case types.OnNewMember:
		var req types.HookNewMemberOptions
		err = json.NewDecoder(r.Body).Decode(&req)
		if err != nil {
			return types.BadRequest(err)
		}

		if req.NewMember == (types.ClusterMemberLocal{}) {
			return types.SmartError(fmt.Errorf("No new member name given for NewMember hook execution"))
		}

		err = intState.Hooks.OnNewMember(ctx, s, req.NewMember)
		if err != nil {
			return types.SmartError(fmt.Errorf("Failed to run hook after system %q has joined the cluster: %w", req.NewMember.Name, err))
		}

	case types.OnDaemonConfigUpdate:
		var req types.DaemonConfig
		err = json.NewDecoder(r.Body).Decode(&req)
		if err != nil {
			return types.BadRequest(err)
		}

		err = intState.Hooks.OnDaemonConfigUpdate(ctx, s, req)
		if err != nil {
			return types.SmartError(fmt.Errorf("Failed to run hook on %q after daemon received local config update: %w", s.Name(), err))
		}

	default:
		return types.SmartError(fmt.Errorf("No valid hook found for the given type"))
	}

	return types.EmptySyncResponse
}
