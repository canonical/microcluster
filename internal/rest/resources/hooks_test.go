package resources

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/canonical/lxd/shared/api"
	"github.com/gorilla/mux"
	"github.com/stretchr/testify/require"
	"github.com/stretchr/testify/suite"

	"github.com/canonical/microcluster/internal/rest/types"
	"github.com/canonical/microcluster/internal/state"
)

type hooksSuite struct {
	suite.Suite
}

func TestHooksSuite(t *testing.T) {
	suite.Run(t, new(hooksSuite))
}

func (t *hooksSuite) Test_hooks() {
	s := &state.InternalState{
		Context:      context.TODO(),
		InternalName: func() string { return "n0" },
		Hooks:        &state.Hooks{},
	}

	var ranHook types.HookType
	var isForce bool
	s.Hooks.PostRemove = func(state state.State, force bool) error {
		ranHook = types.PostRemove
		isForce = force
		return nil
	}

	s.Hooks.PreRemove = func(state state.State, force bool) error {
		ranHook = types.PreRemove
		isForce = force
		return nil
	}

	s.Hooks.OnNewMember = func(state state.State) error {
		ranHook = types.OnNewMember
		return nil
	}

	tests := []struct {
		name      string
		req       any
		hookType  types.HookType
		expectErr bool
	}{
		{
			name:      "Run OnNewMember hook",
			req:       types.HookNewMemberOptions{Name: "n1"},
			hookType:  types.OnNewMember,
			expectErr: false,
		},
		{
			name:      "Run PostRemove hook with force",
			req:       types.HookRemoveMemberOptions{Force: true},
			hookType:  types.PostRemove,
			expectErr: false,
		},
		{
			name:      "Run PostRemove hook without force",
			req:       types.HookRemoveMemberOptions{},
			hookType:  types.PostRemove,
			expectErr: false,
		},
		{
			name:      "Run PreRemove hook with force",
			req:       types.HookRemoveMemberOptions{Force: true},
			hookType:  types.PreRemove,
			expectErr: false,
		},
		{
			name:      "Run PreRemove hook without force",
			req:       types.HookRemoveMemberOptions{},
			hookType:  types.PreRemove,
			expectErr: false,
		},
		{
			name:      "Fail to run any other hook",
			req:       types.HookNewMemberOptions{Name: "n1"},
			hookType:  types.PostBootstrap,
			expectErr: true,
		},
		{
			name:      "Fail to run a nonexistent hook",
			req:       types.HookNewMemberOptions{Name: "n1"},
			hookType:  "this is not a hook type",
			expectErr: true,
		},
		{
			name:      "Fail to run a hook with the wrong payload type",
			req:       types.HookRemoveMemberOptions{Force: true},
			hookType:  types.OnNewMember,
			expectErr: true,
		},
	}

	for i, c := range tests {
		t.T().Logf("%s (case %d)", c.name, i)

		ranHook = ""
		isForce = false
		expectForce := false
		req := &http.Request{}
		payload, ok := c.req.(types.HookRemoveMemberOptions)
		if !ok {
			payload, ok := c.req.(types.HookNewMemberOptions)
			t.True(ok)
			req.Body = io.NopCloser(strings.NewReader(fmt.Sprintf(`{"name": %q}`, payload.Name)))
		} else {
			expectForce = payload.Force
			req.Body = io.NopCloser(strings.NewReader(fmt.Sprintf(`{"force": %v}`, expectForce)))
		}

		req = mux.SetURLVars(req, map[string]string{"hookType": string(c.hookType)})

		response := hooksPost(s, req)
		recorder := httptest.NewRecorder()
		err := response.Render(recorder)
		require.NoError(t.T(), err)

		var resp api.Response
		err = json.NewDecoder(recorder.Result().Body).Decode(&resp)
		require.NoError(t.T(), err)

		if !c.expectErr {
			t.Equal(api.SyncResponse, resp.Type)
			t.Equal(api.Success.String(), resp.Status)
			t.Equal(http.StatusOK, resp.StatusCode)
			t.Equal(c.hookType, ranHook)
			t.Equal(expectForce, isForce)
		} else {
			t.Equal(api.ErrorResponse, resp.Type)
			t.NotEqual(api.Success.String(), resp.Status)
			t.NotEqual(http.StatusOK, resp.StatusCode)
			t.Equal(types.HookType(""), ranHook)
			t.Equal(false, isForce)
		}
	}
}
