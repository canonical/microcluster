package resources

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"sync"
	"testing"

	"github.com/canonical/lxd/shared"
	"github.com/stretchr/testify/require"

	internalState "github.com/canonical/microcluster/v4/internal/state"
	"github.com/canonical/microcluster/v4/internal/trust"
	"github.com/canonical/microcluster/v4/microcluster/types"
)

// trustMember is a fake cluster member that records the truststore entries it is sent.
type trustMember struct {
	server *httptest.Server
	fail   bool

	mu       sync.Mutex
	received []*http.Request
	entries  []types.ClusterMemberLocal
}

func newTrustMember(t *testing.T, clusterCert *shared.CertInfo, fail bool) *trustMember {
	t.Helper()

	m := &trustMember{fail: fail}
	m.server = httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var entry types.ClusterMemberLocal
		err := json.NewDecoder(r.Body).Decode(&entry)
		require.NoError(t, err)

		m.mu.Lock()
		m.received = append(m.received, r)
		m.entries = append(m.entries, entry)
		m.mu.Unlock()

		if m.fail {
			_ = types.InternalError(nil).Render(w, r)
			return
		}

		_ = types.EmptySyncResponse.Render(w, r)
	}))

	m.server.TLS = &tls.Config{
		Certificates: []tls.Certificate{clusterCert.KeyPair()},
		ClientAuth:   tls.RequestClientCert,
	}

	m.server.StartTLS()
	t.Cleanup(m.server.Close)

	return m
}

func (m *trustMember) address(t *testing.T) types.AddrPort {
	t.Helper()

	addr, err := types.ParseAddrPort(m.server.Listener.Addr().String())
	require.NoError(t, err)

	return addr
}

func TestPropagateTrustStoreEntry(t *testing.T) {
	t.Parallel()

	certPath := filepath.Join(t.TempDir(), "certs")
	err := os.MkdirAll(certPath, 0755)
	require.NoError(t, err)

	serverCert, err := shared.KeyPairAndCA(certPath, string(types.ServerCertificateName), shared.CertServer, shared.CertOptions{})
	require.NoError(t, err)

	// The fake members present the cluster certificate, which is what a client pins.
	clusterCert, err := shared.KeyPairAndCA(certPath, string(types.ClusterCertificateName), shared.CertServer, shared.CertOptions{})
	require.NoError(t, err)

	clusterX509, err := clusterCert.PublicKeyX509()
	require.NoError(t, err)

	tests := []struct {
		name      string
		failing   []bool // One fake member per entry, failing or not.
		expectErr bool
	}{
		{name: "every member reachable", failing: []bool{false, false}},
		{name: "one member failing", failing: []bool{false, true}},
		{name: "every member failing", failing: []bool{true, true}, expectErr: true},
		{name: "no other member", failing: []bool{}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			members := make([]*trustMember, 0, len(tt.failing))
			for _, fail := range tt.failing {
				members = append(members, newTrustMember(t, clusterCert, fail))
			}

			// The member being added must not be sent its own entry, even though it is in the store already.
			joiner := newTrustMember(t, clusterCert, false)

			localAddr, err := types.ParseAddrPort("127.0.0.1:9000")
			require.NoError(t, err)

			trustDir := filepath.Join(t.TempDir(), "truststore")
			err = os.MkdirAll(trustDir, 0755)
			require.NoError(t, err)

			remotes := &trust.Remotes{}
			err = remotes.Load(trustDir)
			require.NoError(t, err)

			storeMember := func(name string, addr types.AddrPort) types.Remote {
				return types.Remote{
					Location:    types.Location{Name: name, Address: addr},
					Certificate: types.X509Certificate{Certificate: clusterX509},
				}
			}

			storeEntries := []types.Remote{storeMember("local", localAddr), storeMember("joiner", joiner.address(t))}
			for i, m := range members {
				storeEntries = append(storeEntries, storeMember("m"+string(rune('1'+i)), m.address(t)))
			}

			err = remotes.Add(trustDir, storeEntries...)
			require.NoError(t, err)

			s := &internalState.InternalState{
				Context:             context.Background(),
				InternalAddress:     func() *url.URL { return &url.URL{Scheme: "https", Host: localAddr.String()} },
				InternalServerCert:  func() *shared.CertInfo { return serverCert },
				InternalClusterCert: func() *shared.CertInfo { return clusterCert },
				InternalRemotes:     func() *trust.Remotes { return remotes },
			}

			ctx := types.ContextWithLogger(context.Background(), slog.New(slog.NewTextHandler(io.Discard, nil)))
			entry := types.ClusterMemberLocal{Name: "joiner", Address: joiner.address(t), Certificate: types.X509Certificate{Certificate: clusterX509}}

			err = propagateTrustStoreEntry(ctx, s, entry)
			if tt.expectErr {
				require.Error(t, err)
			} else {
				require.NoError(t, err)
			}

			// The joiner is never contacted.
			joiner.mu.Lock()
			require.Empty(t, joiner.received)
			joiner.mu.Unlock()

			// Every other member is sent the entry exactly once, as a notification.
			for _, m := range members {
				m.mu.Lock()
				require.Len(t, m.received, 1)
				require.Equal(t, http.MethodPost, m.received[0].Method)
				require.Equal(t, "/core/internal/truststore", m.received[0].URL.Path)
				require.Equal(t, types.UserAgentNotifier, m.received[0].Header.Get("User-Agent"))
				require.Equal(t, "joiner", m.entries[0].Name)
				require.Equal(t, joiner.address(t).String(), m.entries[0].Address.String())
				m.mu.Unlock()
			}
		})
	}
}
