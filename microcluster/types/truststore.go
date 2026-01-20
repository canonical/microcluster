package types

import (
	"crypto/x509"

	"github.com/canonical/lxd/shared"
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
