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

	internalTypes "github.com/canonical/microcluster/v2/internal/rest/types"
	"github.com/canonical/microcluster/v2/internal/state"
	"github.com/canonical/microcluster/v2/rest/types"
)

type hooksSuite struct {
	suite.Suite
}

func TestHooksSuite(t *testing.T) {
	suite.Run(t, new(hooksSuite))
}

func (t *hooksSuite) Test_hooks() {
	var ranHook internalTypes.HookType
	var isForce bool
	s := &state.InternalState{
		Context:      context.TODO(),
		InternalName: func() string { return "n0" },
		Hooks: &state.Hooks{
			PostRemove: func(ctx context.Context, state state.State, force bool) error {
				ranHook = internalTypes.PostRemove
				isForce = force
				return nil
			},

			PreRemove: func(ctx context.Context, state state.State, force bool) error {
				ranHook = internalTypes.PreRemove
				isForce = force
				return nil
			},

			OnNewMember: func(ctx context.Context, state state.State, newMember types.ClusterMemberLocal) error {
				ranHook = internalTypes.OnNewMember
				return nil
			},
		},
	}

	tests := []struct {
		name      string
		req       any
		hookType  internalTypes.HookType
		expectErr bool
	}{
		{
			name:      "Run OnNewMember hook",
			req:       internalTypes.HookNewMemberOptions{NewMember: types.ClusterMemberLocal{Name: "n1"}},
			hookType:  internalTypes.OnNewMember,
			expectErr: false,
		},
		{
			name:      "Run PostRemove hook with force",
			req:       internalTypes.HookRemoveMemberOptions{Force: true},
			hookType:  internalTypes.PostRemove,
			expectErr: false,
		},
		{
			name:      "Run PostRemove hook without force",
			req:       internalTypes.HookRemoveMemberOptions{},
			hookType:  internalTypes.PostRemove,
			expectErr: false,
		},
		{
			name:      "Run PreRemove hook with force",
			req:       internalTypes.HookRemoveMemberOptions{Force: true},
			hookType:  internalTypes.PreRemove,
			expectErr: false,
		},
		{
			name:      "Run PreRemove hook without force",
			req:       internalTypes.HookRemoveMemberOptions{},
			hookType:  internalTypes.PreRemove,
			expectErr: false,
		},
		{
			name:      "Fail to run any other hook",
			req:       internalTypes.HookNewMemberOptions{NewMember: types.ClusterMemberLocal{Name: "n1"}},
			hookType:  internalTypes.PostBootstrap,
			expectErr: true,
		},
		{
			name:      "Fail to run a nonexistent hook",
			req:       internalTypes.HookNewMemberOptions{NewMember: types.ClusterMemberLocal{Name: "n1"}},
			hookType:  "this is not a hook type",
			expectErr: true,
		},
		{
			name:      "Fail to run a hook with the wrong payload type",
			req:       internalTypes.HookRemoveMemberOptions{Force: true},
			hookType:  internalTypes.OnNewMember,
			expectErr: true,
		},
	}

	for i, c := range tests {
		t.T().Logf("%s (case %d)", c.name, i)

		ranHook = ""
		isForce = false
		expectForce := false
		req := &http.Request{}
		payload, ok := c.req.(internalTypes.HookRemoveMemberOptions)
		if !ok {
			payload, ok := c.req.(internalTypes.HookNewMemberOptions)
			t.True(ok)
			req.Body = io.NopCloser(strings.NewReader(fmt.Sprintf(`{"new_member": {"name": %q}}`, payload.NewMember.Name)))
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
			t.Equal(internalTypes.HookType(""), ranHook)
			t.Equal(false, isForce)
		}
	}
}
