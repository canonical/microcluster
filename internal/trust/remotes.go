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

	"github.com/canonical/lxd/shared"
	"github.com/canonical/lxd/shared/api"
	"github.com/canonical/lxd/shared/logger"
	"github.com/google/renameio"
	"gopkg.in/yaml.v2"

	"github.com/canonical/microcluster/client"
	internalClient "github.com/canonical/microcluster/internal/rest/client"
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
	Location    `yaml:",inline"`
	Certificate types.X509Certificate `yaml:"certificate"`
}

// Location represents configurable identifying information about a remote.
type Location struct {
	Name    string         `yaml:"name"`
	Address types.AddrPort `yaml:"address"`
}

// Load reads any yaml files in the given directory and parses them into a set of Remotes.
func (r *Remotes) Load(dir string) error {
	r.updateMu.Lock()
	defer r.updateMu.Unlock()

	files, err := os.ReadDir(dir)
	if err != nil {
		return fmt.Errorf("Unable to read trust directory: %q: %w", dir, err)
	}

	remoteData := map[string]Remote{}
	for _, file := range files {
		fileName := file.Name()
		if file.IsDir() || !strings.HasSuffix(fileName, ".yaml") {
			continue
		}

		content, err := os.ReadFile(filepath.Join(dir, fileName))
		if err != nil {
			return fmt.Errorf("Unable to read file %q: %w", fileName, err)
		}

		remote := &Remote{}
		err = yaml.Unmarshal(content, remote)
		if err != nil {
			return fmt.Errorf("Unable to parse yaml for %q: %w", fileName, err)
		}

		if remote.Certificate.Certificate == nil {
			return fmt.Errorf("Failed to parse local record %q. Found empty certificate", remote.Name)
		}

		remoteData[remote.Name] = *remote
	}

	if len(remoteData) == 0 {
		logger.Warn("Failed to parse new remotes from truststore")

		return nil
	}

	r.data = remoteData

	return nil
}

// Add adds a new local cluster member record for the remotes.
func (r *Remotes) Add(dir string, remotes ...Remote) error {
	r.updateMu.Lock()
	defer r.updateMu.Unlock()

	for _, remote := range remotes {
		if remote.Certificate.Certificate == nil {
			return fmt.Errorf("Failed to parse local record %q. Found empty certificate", remote.Name)
		}

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

		err = renameio.WriteFile(path, bytes, 0644)
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

	if len(newRemotes) == 0 {
		return fmt.Errorf("Received empty remotes")
	}

	remoteData := map[string]Remote{}
	for _, remote := range newRemotes {
		newRemote := Remote{
			Location:    Location{Name: remote.Name, Address: remote.Address},
			Certificate: remote.Certificate,
		}

		if remote.Certificate.Certificate == nil {
			return fmt.Errorf("Failed to parse local record %q. Found empty certificate", remote.Name)
		}

		bytes, err := yaml.Marshal(newRemote)
		if err != nil {
			return fmt.Errorf("Failed to parse remote %q to yaml: %w", remote.Name, err)
		}

		remotePath := filepath.Join(dir, fmt.Sprintf("%s.yaml", remote.Name))
		err = renameio.WriteFile(remotePath, bytes, 0644)
		if err != nil {
			return fmt.Errorf("Failed to write %q: %w", remotePath, err)
		}

		remoteData[remote.Name] = newRemote
	}

	allEntries, err := os.ReadDir(dir)
	if err != nil {
		return err
	}

	// Remove any outdated entries.
	for _, entry := range allEntries {
		name, _, _ := strings.Cut(entry.Name(), ".yaml")
		_, ok := remoteData[name]

		if !ok {
			remotePath := filepath.Join(dir, fmt.Sprintf("%s.yaml", name))
			err = os.Remove(remotePath)
			if err != nil {
				return err
			}
		}
	}

	if len(remoteData) == 0 {
		return fmt.Errorf("Failed to parse new remotes")
	}

	r.data = remoteData

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

// Cluster returns a set of clients for every remote, which can be concurrently queried.
func (r *Remotes) Cluster(isNotification bool, serverCert *shared.CertInfo, publicKey *x509.Certificate) (client.Cluster, error) {
	cluster := make(client.Cluster, 0, r.Count()-1)
	for _, addr := range r.Addresses() {
		url := api.NewURL().Scheme("https").Host(addr.String())
		c, err := internalClient.New(*url, serverCert, publicKey, isNotification)
		if err != nil {
			return nil, err
		}

		cluster = append(cluster, client.Client{Client: *c})
	}

	return cluster, nil
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
	for _, remote := range r.data {
		certMap[shared.CertFingerprint(remote.Certificate.Certificate)] = *remote.Certificate.Certificate
	}

	return certMap
}

func (r *Remotes) Count() int {
	r.updateMu.RLock()
	defer r.updateMu.RUnlock()

	return len(r.data)
}

// RemotesByName returns a copy of the list of peers, keyed by each system's name.
func (r *Remotes) RemotesByName() map[string]Remote {
	r.updateMu.RLock()
	defer r.updateMu.RUnlock()

	remoteData := make(map[string]Remote, len(r.data))
	for name, data := range r.data {
		remoteData[name] = data
	}

	return remoteData
}

// URL returns the parsed URL of the Remote.
func (r *Remote) URL() api.URL {
	return *api.NewURL().Scheme("https").Host(r.Address.String())
}
