package resources

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"testing"

	"github.com/canonical/lxd/shared"
	"github.com/canonical/lxd/shared/api"
	"github.com/stretchr/testify/require"

	internalConfig "github.com/canonical/microcluster/v4/internal/config"
	internalState "github.com/canonical/microcluster/v4/internal/state"
	"github.com/canonical/microcluster/v4/internal/trust"
	"github.com/canonical/microcluster/v4/microcluster/types"
)

func TestDaemonConfigGet(t *testing.T) {
	t.Parallel()

	addr, err := types.ParseAddrPort("127.0.0.1:9001")
	require.NoError(t, err)

	serverAddr, err := types.ParseAddrPort("127.0.0.1:9443")
	require.NoError(t, err)

	cfg := internalConfig.NewDaemonConfig(filepath.Join(t.TempDir(), "daemon.yaml"))
	cfg.SetName("m1")
	cfg.SetAddress(addr)
	cfg.SetServers(map[string]types.ServerConfig{
		"metrics.example.com": {Address: serverAddr},
	})
	cfg.SetFailureDomain(7)

	s := &internalState.InternalState{
		LocalConfig: func() *internalConfig.DaemonConfig { return cfg },
	}

	req := &http.Request{
		Method: http.MethodGet,
		URL:    &url.URL{},
	}

	resp := daemonConfigGet(s, req)
	recorder := httptest.NewRecorder()
	err = resp.Render(recorder, req)
	require.NoError(t, err)

	var body api.Response
	err = json.NewDecoder(recorder.Result().Body).Decode(&body)
	require.NoError(t, err)
	require.Equal(t, api.SyncResponse, body.Type)
	require.Equal(t, http.StatusOK, body.StatusCode)

	metadata, err := json.Marshal(body.Metadata)
	require.NoError(t, err)

	var got types.DaemonConfig
	err = json.Unmarshal(metadata, &got)
	require.NoError(t, err)

	require.Equal(t, "m1", got.Name)
	require.Equal(t, "127.0.0.1:9001", got.Address.String())
	require.Equal(t, uint64(7), got.FailureDomain)
	require.NotNil(t, got.Servers)
	require.Contains(t, got.Servers, "metrics.example.com")
	require.Equal(t, "127.0.0.1:9443", got.Servers["metrics.example.com"].Address.String())
}

func TestDaemonServersGetCompatibility(t *testing.T) {
	t.Parallel()

	serverAddr, err := types.ParseAddrPort("127.0.0.1:9443")
	require.NoError(t, err)

	cfg := internalConfig.NewDaemonConfig(filepath.Join(t.TempDir(), "daemon.yaml"))
	cfg.SetServers(map[string]types.ServerConfig{
		"metrics.example.com": {Address: serverAddr},
	})

	s := &internalState.InternalState{
		LocalConfig: func() *internalConfig.DaemonConfig { return cfg },
	}

	req := &http.Request{
		Method: http.MethodGet,
		URL:    &url.URL{},
	}

	resp := daemonServersGet(s, req)
	recorder := httptest.NewRecorder()
	err = resp.Render(recorder, req)
	require.NoError(t, err)

	var body api.Response
	err = json.NewDecoder(recorder.Result().Body).Decode(&body)
	require.NoError(t, err)
	require.Equal(t, api.SyncResponse, body.Type)
	require.Equal(t, http.StatusOK, body.StatusCode)

	metadata, err := json.Marshal(body.Metadata)
	require.NoError(t, err)

	var got map[string]types.ServerConfig
	err = json.Unmarshal(metadata, &got)
	require.NoError(t, err)
	require.Len(t, got, 1)
	require.Contains(t, got, "metrics.example.com")
	require.Equal(t, "127.0.0.1:9443", got["metrics.example.com"].Address.String())
}

func TestValidateDaemonConfigUpdateRejects(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		mutate  func(t *testing.T, cfg *internalConfig.DaemonConfig) types.DaemonConfig
		wantErr string
	}{
		{
			name: "name change",
			mutate: func(t *testing.T, cfg *internalConfig.DaemonConfig) types.DaemonConfig {
				servers := cfg.GetServers()
				fd := cfg.GetFailureDomain()
				return types.DaemonConfig{
					Name:          "m2",
					Address:       cfg.GetAddress(),
					Servers:       servers,
					FailureDomain: fd,
				}
			},
			wantErr: "Name is immutable",
		},
		{
			name: "address change",
			mutate: func(t *testing.T, cfg *internalConfig.DaemonConfig) types.DaemonConfig {
				otherAddr, err := types.ParseAddrPort("127.0.0.1:9002")
				require.NoError(t, err)

				servers := cfg.GetServers()
				fd := cfg.GetFailureDomain()
				return types.DaemonConfig{
					Name:          cfg.GetName(),
					Address:       otherAddr,
					Servers:       servers,
					FailureDomain: fd,
				}
			},
			wantErr: "Address is immutable",
		},
		{
			name: "unknown server",
			mutate: func(t *testing.T, cfg *internalConfig.DaemonConfig) types.DaemonConfig {
				serverAddr, err := types.ParseAddrPort("127.0.0.1:9444")
				require.NoError(t, err)

				fd := cfg.GetFailureDomain()
				servers := map[string]types.ServerConfig{
					"unknown.example.com": {Address: serverAddr},
				}

				return types.DaemonConfig{
					Name:          cfg.GetName(),
					Address:       cfg.GetAddress(),
					Servers:       servers,
					FailureDomain: fd,
				}
			},
			wantErr: "No matching additional listener found",
		},
		{
			name: "address conflict",
			mutate: func(t *testing.T, cfg *internalConfig.DaemonConfig) types.DaemonConfig {
				conflicting, err := types.ParseAddrPort("127.0.0.1:9001")
				require.NoError(t, err)

				fd := cfg.GetFailureDomain()
				servers := map[string]types.ServerConfig{
					"metrics.example.com": {Address: conflicting},
				}

				return types.DaemonConfig{
					Name:          cfg.GetName(),
					Address:       cfg.GetAddress(),
					Servers:       servers,
					FailureDomain: fd,
				}
			},
			wantErr: "already in use",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			addr, err := types.ParseAddrPort("127.0.0.1:9001")
			require.NoError(t, err)
			serverAddr, err := types.ParseAddrPort("127.0.0.1:9443")
			require.NoError(t, err)

			cfg := internalConfig.NewDaemonConfig(filepath.Join(t.TempDir(), "daemon.yaml"))
			cfg.SetName("m1")
			cfg.SetAddress(addr)
			cfg.SetServers(map[string]types.ServerConfig{
				"metrics.example.com": {Address: serverAddr},
			})
			cfg.SetFailureDomain(3)

			updateCalls := 0
			s := &internalState.InternalState{
				LocalConfig: func() *internalConfig.DaemonConfig {
					return cfg
				},
				UpdateServers: func() error {
					updateCalls++
					return nil
				},
				InternalAddress: func() *url.URL {
					return &url.URL{Host: addr.String()}
				},
				InternalExtensionServers: func() []string {
					return []string{"metrics.example.com"}
				},
			}

			req := tt.mutate(t, cfg)

			err = validateDaemonConfigUpdate(s, req)
			require.Error(t, err)
			require.ErrorContains(t, err, tt.wantErr)
			require.Equal(t, 0, updateCalls)
		})
	}
}

func TestDaemonConfigPut(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name            string
		request         func(t *testing.T, cfg *internalConfig.DaemonConfig) types.DaemonConfig
		wantType        api.ResponseType
		wantStatusCode  int
		wantBodyContain string
		wantUpdateCalls int
		verifyState     func(t *testing.T, cfg *internalConfig.DaemonConfig, cfgPath string)
	}{
		{
			name: "applies and persists",
			request: func(t *testing.T, cfg *internalConfig.DaemonConfig) types.DaemonConfig {
				newServerAddr, err := types.ParseAddrPort("127.0.0.1:9555")
				require.NoError(t, err)

				fd := uint64(9)
				servers := map[string]types.ServerConfig{
					"metrics.example.com": {Address: newServerAddr},
				}

				return types.DaemonConfig{
					Name:          cfg.GetName(),
					Address:       cfg.GetAddress(),
					Servers:       servers,
					FailureDomain: fd,
				}
			},
			wantType:        api.SyncResponse,
			wantStatusCode:  http.StatusOK,
			wantUpdateCalls: 1,
			verifyState: func(t *testing.T, cfg *internalConfig.DaemonConfig, cfgPath string) {
				t.Helper()

				require.Equal(t, uint64(9), cfg.GetFailureDomain())
				require.Equal(t, "127.0.0.1:9555", cfg.GetServers()["metrics.example.com"].Address.String())

				reloaded := internalConfig.NewDaemonConfig(cfgPath)
				err := reloaded.Load()
				require.NoError(t, err)
				require.Equal(t, uint64(9), reloaded.GetFailureDomain())
				require.Equal(t, "127.0.0.1:9555", reloaded.GetServers()["metrics.example.com"].Address.String())
			},
		},
		{
			name: "rejects name change",
			request: func(t *testing.T, cfg *internalConfig.DaemonConfig) types.DaemonConfig {
				servers := cfg.GetServers()
				fd := cfg.GetFailureDomain()
				return types.DaemonConfig{
					Name:          "m2",
					Address:       cfg.GetAddress(),
					Servers:       servers,
					FailureDomain: fd,
				}
			},
			wantType:        api.ErrorResponse,
			wantStatusCode:  http.StatusBadRequest,
			wantBodyContain: "Name is immutable",
			wantUpdateCalls: 0,
		},
		{
			name: "omitting servers clears existing servers",
			request: func(t *testing.T, cfg *internalConfig.DaemonConfig) types.DaemonConfig {
				fd := uint64(5)
				return types.DaemonConfig{
					Name:          cfg.GetName(),
					Address:       cfg.GetAddress(),
					FailureDomain: fd,
				}
			},
			wantType:        api.SyncResponse,
			wantStatusCode:  http.StatusOK,
			wantUpdateCalls: 1,
			verifyState: func(t *testing.T, cfg *internalConfig.DaemonConfig, cfgPath string) {
				t.Helper()

				// FailureDomain updated.
				require.Equal(t, uint64(5), cfg.GetFailureDomain())
				// Servers cleared (nil map → empty).
				require.Empty(t, cfg.GetServers())
			},
		},
		{
			name: "omitting failure-domain clears existing value",
			request: func(t *testing.T, cfg *internalConfig.DaemonConfig) types.DaemonConfig {
				newServerAddr, err := types.ParseAddrPort("127.0.0.1:9555")
				require.NoError(t, err)

				servers := map[string]types.ServerConfig{
					"metrics.example.com": {Address: newServerAddr},
				}

				return types.DaemonConfig{
					Name:    cfg.GetName(),
					Address: cfg.GetAddress(),
					Servers: servers,
				}
			},
			wantType:        api.SyncResponse,
			wantStatusCode:  http.StatusOK,
			wantUpdateCalls: 1,
			verifyState: func(t *testing.T, cfg *internalConfig.DaemonConfig, cfgPath string) {
				t.Helper()

				// Servers updated.
				require.Equal(t, "127.0.0.1:9555", cfg.GetServers()["metrics.example.com"].Address.String())
				// FailureDomain cleared (was 3, now 0).
				require.Equal(t, uint64(0), cfg.GetFailureDomain())
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			cfgPath := filepath.Join(t.TempDir(), "daemon.yaml")
			addr, err := types.ParseAddrPort("127.0.0.1:9001")
			require.NoError(t, err)
			serverAddr, err := types.ParseAddrPort("127.0.0.1:9443")
			require.NoError(t, err)

			cfg := internalConfig.NewDaemonConfig(cfgPath)
			cfg.SetName("m1")
			cfg.SetAddress(addr)
			cfg.SetServers(map[string]types.ServerConfig{
				"metrics.example.com": {Address: serverAddr},
			})
			cfg.SetFailureDomain(3)
			err = cfg.Write()
			require.NoError(t, err)

			updateCalls := 0
			s := &internalState.InternalState{
				LocalConfig: func() *internalConfig.DaemonConfig {
					return cfg
				},
				UpdateServers: func() error {
					updateCalls++
					return nil
				},
				InternalAddress: func() *url.URL {
					return &url.URL{Host: addr.String()}
				},
				InternalExtensionServers: func() []string {
					return []string{"metrics.example.com"}
				},
			}

			trustStorePath := filepath.Join(t.TempDir(), "truststore")
			certPath := filepath.Join(t.TempDir(), "certs")
			err = os.MkdirAll(certPath, 0755)
			require.NoError(t, err)
			serverCert, err := shared.KeyPairAndCA(certPath, string(types.ServerCertificateName), shared.CertServer, shared.CertOptions{})
			require.NoError(t, err)
			clusterCert, err := shared.KeyPairAndCA(certPath, string(types.ClusterCertificateName), shared.CertServer, shared.CertOptions{})
			require.NoError(t, err)
			clusterX509, err := clusterCert.PublicKeyX509()
			require.NoError(t, err)

			remotes := &trust.Remotes{}
			err = os.MkdirAll(trustStorePath, 0755)
			require.NoError(t, err)
			err = remotes.Load(trustStorePath)
			require.NoError(t, err)
			err = remotes.Add(trustStorePath, types.Remote{
				Location: types.Location{Name: "m1", Address: addr},
				Certificate: types.X509Certificate{
					Certificate: clusterX509,
				},
			})
			require.NoError(t, err)

			s.InternalServerCert = func() *shared.CertInfo {
				return serverCert
			}

			s.InternalClusterCert = func() *shared.CertInfo {
				return clusterCert
			}

			s.InternalRemotes = func() *trust.Remotes {
				return remotes
			}

			reqBody := tt.request(t, cfg)
			body, err := json.Marshal(reqBody)
			require.NoError(t, err)

			req := &http.Request{
				Method: http.MethodPut,
				URL:    &url.URL{},
				Body:   io.NopCloser(bytes.NewReader(body)),
			}

			resp := daemonConfigPut(s, req)
			recorder := httptest.NewRecorder()
			err = resp.Render(recorder, req)
			require.NoError(t, err)

			var apiResp api.Response
			err = json.NewDecoder(recorder.Result().Body).Decode(&apiResp)
			require.NoError(t, err)
			require.Equal(t, tt.wantType, apiResp.Type)
			require.Equal(t, tt.wantStatusCode, recorder.Result().StatusCode)
			require.Equal(t, tt.wantUpdateCalls, updateCalls)

			if tt.wantBodyContain != "" {
				require.Contains(t, recorder.Body.String(), tt.wantBodyContain)
			}

			if tt.verifyState != nil {
				tt.verifyState(t, cfg, cfgPath)
			}
		})
	}
}

func TestDaemonConfigPatch(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name            string
		request         func(t *testing.T, cfg *internalConfig.DaemonConfig) types.DaemonConfigPatch
		wantType        api.ResponseType
		wantStatusCode  int
		wantBodyContain string
		wantUpdateCalls int
		verifyState     func(t *testing.T, cfg *internalConfig.DaemonConfig, cfgPath string)
	}{
		{
			name: "applies and persists",
			request: func(t *testing.T, cfg *internalConfig.DaemonConfig) types.DaemonConfigPatch {
				newServerAddr, err := types.ParseAddrPort("127.0.0.1:9555")
				require.NoError(t, err)

				fd := uint64(7)
				servers := map[string]types.ServerConfig{
					"metrics.example.com": {Address: newServerAddr},
				}

				return types.DaemonConfigPatch{
					Servers:       &servers,
					FailureDomain: &fd,
				}
			},
			wantType:        api.SyncResponse,
			wantStatusCode:  http.StatusOK,
			wantUpdateCalls: 1,
			verifyState: func(t *testing.T, cfg *internalConfig.DaemonConfig, cfgPath string) {
				t.Helper()

				require.Equal(t, uint64(7), cfg.GetFailureDomain())
				require.Equal(t, "127.0.0.1:9555", cfg.GetServers()["metrics.example.com"].Address.String())

				reloaded := internalConfig.NewDaemonConfig(cfgPath)
				err := reloaded.Load()
				require.NoError(t, err)
				require.Equal(t, uint64(7), reloaded.GetFailureDomain())
				require.Equal(t, "127.0.0.1:9555", reloaded.GetServers()["metrics.example.com"].Address.String())
			},
		},
		{
			name: "omitting servers preserves existing servers",
			request: func(t *testing.T, cfg *internalConfig.DaemonConfig) types.DaemonConfigPatch {
				fd := uint64(5)
				return types.DaemonConfigPatch{
					FailureDomain: &fd,
				}
			},
			wantType:        api.SyncResponse,
			wantStatusCode:  http.StatusOK,
			wantUpdateCalls: 1,
			verifyState: func(t *testing.T, cfg *internalConfig.DaemonConfig, cfgPath string) {
				t.Helper()

				// FailureDomain updated.
				require.Equal(t, uint64(5), cfg.GetFailureDomain())
				// Existing servers untouched.
				require.Contains(t, cfg.GetServers(), "metrics.example.com")
			},
		},
		{
			name: "omitting failure-domain preserves existing value",
			request: func(t *testing.T, cfg *internalConfig.DaemonConfig) types.DaemonConfigPatch {
				newServerAddr, err := types.ParseAddrPort("127.0.0.1:9555")
				require.NoError(t, err)

				servers := map[string]types.ServerConfig{
					"metrics.example.com": {Address: newServerAddr},
				}

				return types.DaemonConfigPatch{
					Servers: &servers,
				}
			},
			wantType:        api.SyncResponse,
			wantStatusCode:  http.StatusOK,
			wantUpdateCalls: 1,
			verifyState: func(t *testing.T, cfg *internalConfig.DaemonConfig, cfgPath string) {
				t.Helper()

				// Servers updated.
				require.Equal(t, "127.0.0.1:9555", cfg.GetServers()["metrics.example.com"].Address.String())
				// FailureDomain untouched (was 3).
				require.Equal(t, uint64(3), cfg.GetFailureDomain())
			},
		},
		{
			name: "setting failure-domain zero clears existing value",
			request: func(t *testing.T, cfg *internalConfig.DaemonConfig) types.DaemonConfigPatch {
				fd := uint64(0)
				return types.DaemonConfigPatch{
					FailureDomain: &fd,
				}
			},
			wantType:        api.SyncResponse,
			wantStatusCode:  http.StatusOK,
			wantUpdateCalls: 1,
			verifyState: func(t *testing.T, cfg *internalConfig.DaemonConfig, cfgPath string) {
				t.Helper()

				require.Equal(t, uint64(0), cfg.GetFailureDomain())
			},
		},
		{
			name: "setting servers empty clears existing servers",
			request: func(t *testing.T, cfg *internalConfig.DaemonConfig) types.DaemonConfigPatch {
				servers := map[string]types.ServerConfig{}
				return types.DaemonConfigPatch{
					Servers: &servers,
				}
			},
			wantType:        api.SyncResponse,
			wantStatusCode:  http.StatusOK,
			wantUpdateCalls: 1,
			verifyState: func(t *testing.T, cfg *internalConfig.DaemonConfig, cfgPath string) {
				t.Helper()

				require.Empty(t, cfg.GetServers())
			},
		},
		{
			name: "rejects unknown server name",
			request: func(t *testing.T, cfg *internalConfig.DaemonConfig) types.DaemonConfigPatch {
				serverAddr, err := types.ParseAddrPort("127.0.0.1:9555")
				require.NoError(t, err)

				servers := map[string]types.ServerConfig{
					"unknown.example.com": {Address: serverAddr},
				}

				return types.DaemonConfigPatch{
					Servers: &servers,
				}
			},
			wantType:        api.ErrorResponse,
			wantStatusCode:  http.StatusBadRequest,
			wantBodyContain: "No matching additional listener found",
			wantUpdateCalls: 0,
		},
		{
			name: "rejects server address conflict",
			request: func(t *testing.T, cfg *internalConfig.DaemonConfig) types.DaemonConfigPatch {
				conflicting, err := types.ParseAddrPort("127.0.0.1:9001")
				require.NoError(t, err)

				servers := map[string]types.ServerConfig{
					"metrics.example.com": {Address: conflicting},
				}

				return types.DaemonConfigPatch{
					Servers: &servers,
				}
			},
			wantType:        api.ErrorResponse,
			wantStatusCode:  http.StatusBadRequest,
			wantBodyContain: "already in use",
			wantUpdateCalls: 0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			cfgPath := filepath.Join(t.TempDir(), "daemon.yaml")
			addr, err := types.ParseAddrPort("127.0.0.1:9001")
			require.NoError(t, err)
			serverAddr, err := types.ParseAddrPort("127.0.0.1:9443")
			require.NoError(t, err)

			cfg := internalConfig.NewDaemonConfig(cfgPath)
			cfg.SetName("m1")
			cfg.SetAddress(addr)
			cfg.SetServers(map[string]types.ServerConfig{
				"metrics.example.com": {Address: serverAddr},
			})
			cfg.SetFailureDomain(3)
			err = cfg.Write()
			require.NoError(t, err)

			updateCalls := 0
			s := &internalState.InternalState{
				LocalConfig: func() *internalConfig.DaemonConfig {
					return cfg
				},
				UpdateServers: func() error {
					updateCalls++
					return nil
				},
				InternalAddress: func() *url.URL {
					return &url.URL{Host: addr.String()}
				},
				InternalExtensionServers: func() []string {
					return []string{"metrics.example.com"}
				},
			}

			trustStorePath := filepath.Join(t.TempDir(), "truststore")
			certPath := filepath.Join(t.TempDir(), "certs")
			err = os.MkdirAll(certPath, 0755)
			require.NoError(t, err)
			serverCert, err := shared.KeyPairAndCA(certPath, string(types.ServerCertificateName), shared.CertServer, shared.CertOptions{})
			require.NoError(t, err)
			clusterCert, err := shared.KeyPairAndCA(certPath, string(types.ClusterCertificateName), shared.CertServer, shared.CertOptions{})
			require.NoError(t, err)
			clusterX509, err := clusterCert.PublicKeyX509()
			require.NoError(t, err)

			remotes := &trust.Remotes{}
			err = os.MkdirAll(trustStorePath, 0755)
			require.NoError(t, err)
			err = remotes.Load(trustStorePath)
			require.NoError(t, err)
			err = remotes.Add(trustStorePath, types.Remote{
				Location: types.Location{Name: "m1", Address: addr},
				Certificate: types.X509Certificate{
					Certificate: clusterX509,
				},
			})
			require.NoError(t, err)

			s.InternalServerCert = func() *shared.CertInfo {
				return serverCert
			}

			s.InternalClusterCert = func() *shared.CertInfo {
				return clusterCert
			}

			s.InternalRemotes = func() *trust.Remotes {
				return remotes
			}

			reqBody := tt.request(t, cfg)
			body, err := json.Marshal(reqBody)
			require.NoError(t, err)

			req := &http.Request{
				Method: http.MethodPatch,
				URL:    &url.URL{},
				Body:   io.NopCloser(bytes.NewReader(body)),
			}

			resp := daemonConfigPatch(s, req)
			recorder := httptest.NewRecorder()
			err = resp.Render(recorder, req)
			require.NoError(t, err)

			var apiResp api.Response
			err = json.NewDecoder(recorder.Result().Body).Decode(&apiResp)
			require.NoError(t, err)
			require.Equal(t, tt.wantType, apiResp.Type)
			require.Equal(t, tt.wantStatusCode, recorder.Result().StatusCode)
			require.Equal(t, tt.wantUpdateCalls, updateCalls)

			if tt.wantBodyContain != "" {
				require.Contains(t, recorder.Body.String(), tt.wantBodyContain)
			}

			if tt.verifyState != nil {
				tt.verifyState(t, cfg, cfgPath)
			}
		})
	}
}
