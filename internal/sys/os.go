package sys

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/lxc/lxd/shared"
	"github.com/lxc/lxd/shared/api"
)

// OS contains fields and methods for interacting with the state directory.
type OS struct {
	StateDir    string
	DatabaseDir string
	TrustDir    string
	LogFile     string
}

// DefaultOS returns a fresh uninitialized OS instance with default values.
func DefaultOS(stateDir string, createDir bool) (*OS, error) {
	if stateDir == "" {
		stateDir = os.Getenv(StateDir)
	}

	// TODO: Configurable log file path.

	os := &OS{
		StateDir:    stateDir,
		DatabaseDir: filepath.Join(stateDir, "database"),
		TrustDir:    filepath.Join(stateDir, "truststore"),
		LogFile:     "",
	}

	err := os.init(createDir)
	if err != nil {
		return nil, err
	}

	return os, nil
}

func (s *OS) init(createDir bool) error {
	dirs := []struct {
		path string
		mode os.FileMode
	}{
		{s.StateDir, 0711},
		{s.DatabaseDir, 0700},
		{s.TrustDir, 0700},
	}

	for _, dir := range dirs {
		// If we are not creating the directories, ensure they still exist.
		if !createDir {
			_, err := os.Stat(dir.path)
			if err != nil {
				return fmt.Errorf("Unable to get state dir information: %w", err)
			}

			return nil
		}

		err := os.MkdirAll(dir.path, dir.mode)
		if err != nil {
			if !os.IsExist(err) {
				return fmt.Errorf("Failed to init dir %q: %w", dir.path, err)
			}

			err = os.Chmod(dir.path, dir.mode)
			if err != nil && !os.IsNotExist(err) {
				return fmt.Errorf("Failed to chmod dir %q: %w", dir.path, err)
			}
		}
	}

	return nil
}

// ControlSocket returns the full path to the control.socket file that this daemon is listening on.
func (s *OS) ControlSocket() api.URL {
	return *api.NewURL().Scheme("http").Host(filepath.Join(s.StateDir, "control.socket"))
}

// DatabasePath returns the path of the database file managed by dqlite.
func (s *OS) DatabasePath() string {
	return filepath.Join(s.DatabaseDir, "db.bin")
}

// ServerCert gets the local server certificate from the state directory.
func (s *OS) ServerCert() (*shared.CertInfo, error) {
	if !shared.PathExists(filepath.Join(s.StateDir, "server.crt")) {
		return nil, fmt.Errorf("Failed to get server.crt from directory %q", s.StateDir)
	}

	cert, err := shared.KeyPairAndCA(s.StateDir, "server", shared.CertServer, true)
	if err != nil {
		return nil, fmt.Errorf("failed to load TLS certificate: %w", err)
	}

	return cert, nil
}

// ClusterCert gets the local cluster certificate from the state directory.
func (s *OS) ClusterCert() (*shared.CertInfo, error) {
	if !shared.PathExists(filepath.Join(s.StateDir, "cluster.crt")) {
		return nil, fmt.Errorf("Failed to get cluster.crt from directory %q", s.StateDir)
	}

	cert, err := shared.KeyPairAndCA(s.StateDir, "cluster", shared.CertServer, true)
	if err != nil {
		return nil, fmt.Errorf("failed to load TLS certificate: %w", err)
	}

	return cert, nil
}
