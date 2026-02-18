package client

import (
	"context"
	"time"

	"github.com/canonical/lxd/shared/api"

	"github.com/canonical/microcluster/v3/microcluster/types"
)

// GetDaemonConfig retrieves local daemon configuration.
func GetDaemonConfig(ctx context.Context, c types.Client) (*types.DaemonConfig, error) {
	queryCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()

	endpoint := api.NewURL().Path("daemon", "config")
	config := types.DaemonConfig{}
	err := c.Query(queryCtx, "GET", types.PublicEndpoint, &endpoint.URL, nil, &config)
	if err != nil {
		return nil, err
	}

	return &config, nil
}

// UpdateDaemonConfig updates local daemon configuration.
func UpdateDaemonConfig(ctx context.Context, c types.Client, config types.DaemonConfig) error {
	queryCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()

	endpoint := api.NewURL().Path("daemon", "config")
	return c.Query(queryCtx, "PUT", types.PublicEndpoint, &endpoint.URL, config, nil)
}

// PatchDaemonConfig partially updates local daemon configuration.
func PatchDaemonConfig(ctx context.Context, c types.Client, config types.DaemonConfigPatch) error {
	queryCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()

	endpoint := api.NewURL().Path("daemon", "config")
	return c.Query(queryCtx, "PATCH", types.PublicEndpoint, &endpoint.URL, config, nil)
}

// UpdateServers updates the additional servers config.
func UpdateServers(ctx context.Context, c types.Client, config map[string]types.ServerConfig) error {
	queryCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()

	endpoint := api.NewURL().Path("daemon", "servers")
	return c.Query(queryCtx, "PUT", types.PublicEndpoint, &endpoint.URL, config, nil)
}
