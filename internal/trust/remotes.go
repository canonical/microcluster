package trust

import (
	"crypto/x509"
	"errors"
	"fmt"
	"math/rand"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/lxc/lxd/shared"
	"github.com/lxc/lxd/shared/api"
	"gopkg.in/yaml.v2"

	internalTypes "github.com/canonical/microcluster/internal/rest/types"
	"github.com/canonical/microcluster/rest/types"
)

// Remotes is a convenient alias as we will often deal with groups of yaml files.
type Remotes struct {
	data     map[string]Remote
	updateMu sync.RWMutex
}

// Remote represents a yaml file with credentials to be read by the daemon.
type Remote struct {
	Name        string                `yaml:"name"`
	Address     types.AddrPort        `yaml:"address"`
	Certificate types.X509Certificate `yaml:"certificate"`
}

// Load reads any yaml files in the given directory and parses them into a set of Remotes.
func Load(dir string) (*Remotes, error) {
	files, err := os.ReadDir(dir)
	if err != nil {
		return nil, fmt.Errorf("Unable to read trust directory: %q: %w", dir, err)
	}

	remotes := &Remotes{data: map[string]Remote{}}
	for _, file := range files {
		fileName := file.Name()
		if file.IsDir() || !strings.HasSuffix(fileName, ".yaml") {
			continue
		}

		content, err := os.ReadFile(filepath.Join(dir, fileName))
		if err != nil {
			return nil, fmt.Errorf("Unable to read file %q: %w", fileName, err)
		}

		remote := &Remote{}
		err = yaml.Unmarshal(content, remote)
		if err != nil {
			return nil, fmt.Errorf("Unable to parse yaml for %q: %w", fileName, err)
		}

		remotes.data[remote.Name] = *remote
	}

	return remotes, nil
}

// Add adds a new local cluster member record for the remotes.
func (r *Remotes) Add(dir string, remotes ...Remote) error {
	r.updateMu.Lock()
	defer r.updateMu.Unlock()

	for _, remote := range remotes {
		_, ok := r.data[remote.Name]
		if ok {
			return fmt.Errorf("A remote with name %q already exists", remote.Name)
		}
		bytes, err := yaml.Marshal(remote)
		if err != nil {
			return fmt.Errorf("Failed to parse remote %q to yaml: %w", remote.Name, err)
		}

		path := filepath.Join(dir, fmt.Sprintf("%s.yaml", remote.Name))
		_, err = os.Stat(path)
		if err == nil {
			return fmt.Errorf("Remote at %q already exists", path)
		}

		if !errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("Failed to check remote path %q: %w", path, err)
		}

		err = os.WriteFile(path, bytes, 0644)
		if err != nil {
			return fmt.Errorf("Failed to write %q: %w", path, err)
		}

		// Add the remote manually so we can use it right away without waiting for inotify.
		r.data[remote.Name] = remote
	}

	return nil
}

// Replace replaces the in-memory and locally stored remotes with the given list from the database.
func (r *Remotes) Replace(dir string, newRemotes ...internalTypes.ClusterMember) error {
	r.updateMu.Lock()
	defer r.updateMu.Unlock()

	for _, remote := range r.data {
		remotePath := filepath.Join(dir, fmt.Sprintf("%s.yaml", remote.Name))
		err := os.Remove(remotePath)
		if err != nil {
			return err
		}
	}

	r.data = map[string]Remote{}
	for _, remote := range newRemotes {
		newRemote := Remote{Name: remote.Name, Address: remote.Address, Certificate: remote.Certificate}
		bytes, err := yaml.Marshal(newRemote)
		if err != nil {
			return fmt.Errorf("Failed to parse remote %q to yaml: %w", remote.Name, err)
		}

		remotePath := filepath.Join(dir, fmt.Sprintf("%s.yaml", remote.Name))
		err = os.WriteFile(remotePath, bytes, 0644)
		if err != nil {
			return fmt.Errorf("Failed to write %q: %w", remotePath, err)
		}

		r.data[remote.Name] = newRemote
	}

	return nil
}

// SelectRandom returns a random remote.
func (r *Remotes) SelectRandom() *Remote {
	r.updateMu.RLock()
	defer r.updateMu.RUnlock()

	allRemotes := make([]Remote, 0, len(r.data))
	for _, r := range r.data {
		allRemotes = append(allRemotes, r)
	}

	return &allRemotes[rand.Intn(len(allRemotes))]
}

// Addresses returns just the host:port addresses of the remotes.
func (r *Remotes) Addresses() map[string]types.AddrPort {
	r.updateMu.RLock()
	defer r.updateMu.RUnlock()

	addrs := map[string]types.AddrPort{}
	for _, remote := range r.data {
		addrs[remote.Name] = remote.Address
	}

	return addrs
}

// RemoteByAddress returns a Remote matching the given host address (or nil if none are found).
func (r *Remotes) RemoteByAddress(addrPort types.AddrPort) *Remote {
	r.updateMu.RLock()
	defer r.updateMu.RUnlock()

	for _, remote := range r.data {
		if remote.Address.String() == addrPort.String() {
			return &remote
		}
	}

	return nil
}

// RemoteByCertificateFingerprint returns a remote whose certificate fingerprint matches the provided fingerprint.
func (r *Remotes) RemoteByCertificateFingerprint(fingerprint string) *Remote {
	r.updateMu.RLock()
	defer r.updateMu.RUnlock()

	for _, remote := range r.data {
		if fingerprint == shared.CertFingerprint(remote.Certificate.Certificate) {
			return &remote
		}
	}

	return nil
}

// Certificates returns a map of remotes certificates by fingerprint.
func (r *Remotes) Certificates() map[string]types.X509Certificate {
	r.updateMu.RLock()
	defer r.updateMu.RUnlock()

	certMap := map[string]types.X509Certificate{}
	for _, remote := range r.data {
		certMap[shared.CertFingerprint(remote.Certificate.Certificate)] = remote.Certificate
	}

	return certMap
}

// CertificatesNative returns the Certificates map with values as native x509.Certificate type.
func (r *Remotes) CertificatesNative() map[string]x509.Certificate {
	r.updateMu.RLock()
	defer r.updateMu.RUnlock()

	certMap := map[string]x509.Certificate{}
	for k, v := range r.Certificates() {
		certMap[k] = *v.Certificate
	}

	return certMap
}

func (r *Remotes) Count() int {
	r.updateMu.RLock()
	defer r.updateMu.RUnlock()

	return len(r.data)
}

func (r *Remotes) RemotesByName() map[string]Remote {
	r.updateMu.RLock()
	defer r.updateMu.RUnlock()

	return r.data
}

// URL returns the parsed URL of the Remote.
func (r *Remote) URL() api.URL {
	return *api.NewURL().Scheme("https").Host(r.Address.String())
}
