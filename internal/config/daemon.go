package config

import (
	"fmt"
	"os"
	"sync"

	"gopkg.in/yaml.v3"

	"github.com/canonical/microcluster/rest/types"
)

// DaemonConfig wraps the daemon's config with get, set and lock capabilities.
type DaemonConfig struct {
	// Path of the daemon.yaml file.
	path string

	// Lock the daemon config for read and write operations.
	lock *sync.RWMutex

	// The actual configuration.
	config *types.DaemonConfig
}

// NewDaemonConfig returns an initialised version of the daemon's config.
// The implementation is thread safe so the same in memory representation of the config
// can be consumed both internally in the daemon and it's API endpoints.
// The config has to be written to file proactively so when setting a config setting
// it doesn't automatically get propagated to the underlying file.
func NewDaemonConfig(path string) *DaemonConfig {
	return &DaemonConfig{
		path: path,
		lock: &sync.RWMutex{},
		config: &types.DaemonConfig{
			Servers: make(map[string]types.ServerConfig),
		},
	}
}

// Load loads the daemon's config from its path.
func (d *DaemonConfig) Load() error {
	d.lock.Lock()
	defer d.lock.Unlock()

	data, err := os.ReadFile(d.path)
	if err != nil {
		return fmt.Errorf("Failed to load daemon config: %w", err)
	}

	err = yaml.Unmarshal(data, d.config)
	if err != nil {
		return fmt.Errorf("Failed to parse daemon config from yaml: %w", err)
	}

	return nil
}

// Write writes the daemon's config to its path.
func (d *DaemonConfig) Write() error {
	d.lock.Lock()
	defer d.lock.Unlock()

	bytes, err := yaml.Marshal(d.config)
	if err != nil {
		return fmt.Errorf("Failed to parse daemon config to yaml: %w", err)
	}

	err = os.WriteFile(d.path, bytes, 0644)
	if err != nil {
		return fmt.Errorf("Failed to write daemon configuration yaml: %w", err)
	}

	return nil
}

// GetName returns the daemon's name.
func (d *DaemonConfig) GetName() string {
	d.lock.RLock()
	defer d.lock.RUnlock()

	return d.config.Name
}

// GetAddress returns the daemon's address.
func (d *DaemonConfig) GetAddress() types.AddrPort {
	d.lock.RLock()
	defer d.lock.RUnlock()

	return d.config.Address
}

// GetServers returns the daemon's additional listener configs.
func (d *DaemonConfig) GetServers() map[string]types.ServerConfig {
	d.lock.RLock()
	defer d.lock.RUnlock()

	// Create a deep copy to not return the reference to the original map.
	serverConfigCopy := make(map[string]types.ServerConfig, len(d.config.Servers))
	for k, v := range d.config.Servers {
		serverConfigCopy[k] = v
	}

	return serverConfigCopy
}

// SetName sets the daemon's name.
func (d *DaemonConfig) SetName(name string) {
	d.lock.Lock()
	defer d.lock.Unlock()

	d.config.Name = name
}

// SetAddress sets the daemon's address.
func (d *DaemonConfig) SetAddress(address types.AddrPort) {
	d.lock.Lock()
	defer d.lock.Unlock()

	d.config.Address = address
}

// SetServers sets the daemon's additional listener configs.
func (d *DaemonConfig) SetServers(servers map[string]types.ServerConfig) {
	d.lock.Lock()
	defer d.lock.Unlock()

	d.config.Servers = servers
}
