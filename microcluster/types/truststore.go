package types

import (
	"crypto/x509"
	"net/url"

	"github.com/canonical/lxd/shared"
	"github.com/canonical/lxd/shared/api"
)

// Store represents a local truststore.
type Store interface {
	// A list of clients for each of the remotes.
	RemoteClients(isNotification bool, serverCert *shared.CertInfo, publicKey *x509.Certificate) (Clients, error)

	// All remotes keyed by their name.
	RemotesByName() map[string]Remote

	// A specific remote filtered by its address.
	RemoteByAddress(addrPort AddrPort) *Remote

	// All remotes' addresses keyed by their name.
	RemoteAddresses() map[string]AddrPort

	// All remotes' certificates keyed by their name.
	RemoteCertificates() map[string]X509Certificate

	// Same as RemoteCertificates but using the standard libraries type.
	RemoteCertificatesNative() map[string]x509.Certificate

	// The total amount of remotes in the truststore.
	Count() int

	// Add a new remote to the truststore.
	Add(dir string, remotes ...Remote) error

	// Replace remotes in the truststore.
	Replace(dir string, newRemotes ...ClusterMember) error
}

// Location represents configurable identifying information about a remote.
type Location struct {
	Name    string   `yaml:"name"`
	Address AddrPort `yaml:"address"`
}

// Remote represents a yaml file with credentials to be read by the daemon.
type Remote struct {
	Location    `yaml:",inline"`
	Certificate X509Certificate `yaml:"certificate"`
}

// URL returns the parsed URL of the Remote.
func (r *Remote) URL() *url.URL {
	return &api.NewURL().Scheme("https").Host(r.Address.String()).URL
}
