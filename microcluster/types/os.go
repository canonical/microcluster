package types

import (
	"net/url"

	"github.com/canonical/lxd/shared"
)

// OS represents Microcluster's state on disk.
type OS interface {
	StateDir() string
	DatabaseDir() string
	TrustDir() string
	CertificatesDir() string
	DatabasePath() string
	ControlSocket() *url.URL
	ControlSocketPath() string
	IsControlSocketPresent() (bool, error)
	ServerCert() (*shared.CertInfo, error)
	ClusterCert() (*shared.CertInfo, error)
}
