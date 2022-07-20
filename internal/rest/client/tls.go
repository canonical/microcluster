package client

import (
	"crypto/tls"
	"crypto/x509"
	"fmt"

	"github.com/lxc/lxd/shared"
)

// TLSClientConfig returns a TLS configuration suitable for establishing horizontal and vertical connections.
// clientCert contains the private key pair for the client. remoteCert is the public
// key of the server we are connecting to.
func TLSClientConfig(clientCert *shared.CertInfo, remoteCert *x509.Certificate) (*tls.Config, error) {
	if clientCert == nil {
		return nil, fmt.Errorf("Invalid client certificate")
	}

	if remoteCert == nil {
		return nil, fmt.Errorf("Invalid remote public key")
	}

	keypair := clientCert.KeyPair()
	config := shared.InitTLSConfig()
	config.Certificates = []tls.Certificate{keypair}

	// Add the public key to the CA pool to make it trusted.
	config.RootCAs = x509.NewCertPool()
	remoteCert.IsCA = true
	remoteCert.KeyUsage = x509.KeyUsageCertSign
	config.RootCAs.AddCert(remoteCert)

	// Always use public key DNS name rather than server cert, so that it matches.
	if len(remoteCert.DNSNames) > 0 {
		config.ServerName = remoteCert.DNSNames[0]
	}

	return config, nil
}
