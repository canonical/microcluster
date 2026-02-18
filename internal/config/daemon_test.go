package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/canonical/microcluster/v3/microcluster/types"
)

func TestDaemonConfigFailureDomainDefaultsToZeroWhenUnset(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "daemon.yaml")

	content := `name: m1
address: 127.0.0.1:9001
servers: {}
`

	err := os.WriteFile(path, []byte(content), 0600)
	require.NoError(t, err)

	cfg := NewDaemonConfig(path)

	err = cfg.Load()
	require.NoError(t, err)

	require.Equal(t, uint64(0), cfg.GetFailureDomain())
}

func TestDaemonConfigFailureDomainRoundTrip(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "daemon.yaml")

	cfg := NewDaemonConfig(path)
	addr, err := types.ParseAddrPort("127.0.0.1:9001")
	require.NoError(t, err)

	cfg.SetName("m1")
	cfg.SetAddress(addr)
	cfg.SetServers(map[string]types.ServerConfig{})
	cfg.SetFailureDomain(42)

	err = cfg.Write()
	require.NoError(t, err)

	content, err := os.ReadFile(path)
	require.NoError(t, err)
	require.True(t, strings.Contains(string(content), "failure-domain: 42"))

	reloaded := NewDaemonConfig(path)
	err = reloaded.Load()
	require.NoError(t, err)
	require.Equal(t, uint64(42), reloaded.GetFailureDomain())
}
