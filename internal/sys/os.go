package sys

import (
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"

	"github.com/canonical/lxd/shared"
	"github.com/canonical/lxd/shared/api"

	"github.com/canonical/microcluster/v4/microcluster/types"
)

// OS contains fields and methods for interacting with the state directory.
type OS struct {
	stateDir        string
	databaseDir     string
	trustDir        string
	certificatesDir string
	logFile         string
}

// DefaultOS returns a fresh uninitialized OS instance with default values.
func DefaultOS(stateDir string, createDir bool) (types.OS, error) {
	if stateDir == "" {
		stateDir = os.Getenv(StateDir)
	}

	// TODO: Configurable log file path.

	os := &OS{
		stateDir:        stateDir,
		databaseDir:     filepath.Join(stateDir, "database"),
		trustDir:        filepath.Join(stateDir, "truststore"),
		certificatesDir: filepath.Join(stateDir, "certificates"),
		logFile:         "",
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
		{s.stateDir, 0711},
		{s.databaseDir, 0700},
		{s.trustDir, 0700},
		{s.certificatesDir, 0700},
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

// IsControlSocketPresent determines if the control socket is present and
// accessible.
func (s *OS) IsControlSocketPresent() (bool, error) {
	socketPath := s.ControlSocketPath()
	_, err := os.Stat(socketPath)

	if err == nil {
		return true, nil
	}

	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}

	return false, err
}

// ControlSocket returns the full path to the control.socket file that this daemon is listening on.
func (s *OS) ControlSocket() *url.URL {
	return &api.NewURL().Scheme("http").Host(s.ControlSocketPath()).URL
}

// ControlSocketPath returns the filesystem path to the control socket.
func (s *OS) ControlSocketPath() string {
	return filepath.Join(s.stateDir, "control.socket")
}

// DatabasePath returns the path of the database file managed by dqlite.
func (s *OS) DatabasePath() string {
	return filepath.Join(s.databaseDir, "db.bin")
}

// ServerCert gets the local server certificate from the state directory.
func (s *OS) ServerCert() (*shared.CertInfo, error) {
	// Make sure to populate the certificates SAN.
	cert, err := shared.KeyPairAndCA(s.stateDir, string(types.ServerCertificateName), shared.CertServer, shared.CertOptions{AddHosts: true})
	if err != nil {
		return nil, fmt.Errorf("Failed to load TLS certificate: %w", err)
	}

	return cert, nil
}

// ClusterCert gets the local cluster certificate from the state directory.
func (s *OS) ClusterCert() (*shared.CertInfo, error) {
	cert, err := shared.KeyPairAndCA(s.stateDir, string(types.ClusterCertificateName), shared.CertServer, shared.CertOptions{})
	if err != nil {
		return nil, fmt.Errorf("Failed to load TLS certificate: %w", err)
	}

	return cert, nil
}

// StateDir gets the state's base directory.
func (s *OS) StateDir() string {
	return s.stateDir
}

// DatabaseDir gets the state's database directory.
func (s *OS) DatabaseDir() string {
	return s.databaseDir
}

// TrustDir gets the state's trust directory.
func (s *OS) TrustDir() string {
	return s.trustDir
}

// CertificatesDir gets the state's certificates directory.
func (s *OS) CertificatesDir() string {
	return s.certificatesDir
}
