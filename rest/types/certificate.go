package types

import (
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"fmt"
)

// X509Certificate is a json/yaml marshallable/unmarshallable type wrapper for x509.Certificate.
type X509Certificate struct {
	*x509.Certificate
}

func ParseX509Certificate(certStr string) (*X509Certificate, error) {
	block, _ := pem.Decode([]byte(certStr))
	if block == nil {
		return nil, fmt.Errorf("Failed to decode certificate")
	}

	cert, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		return nil, err
	}

	return &X509Certificate{Certificate: cert}, nil
}

// String returns the x509.Certificate as a PEM encoded string.
func (c X509Certificate) String() string {
	if c.Certificate == nil {
		return ""
	}

	block := &pem.Block{
		Type:  "CERTIFICATE",
		Bytes: c.Raw,
	}

	return string(pem.EncodeToMemory(block))
}

// MarshalJSON implements json.Marshaler for X509Certificate.
func (c X509Certificate) MarshalJSON() ([]byte, error) {
	return json.Marshal(c.String())
}

// MarshalYAML implements yaml.Marshaller for X509Certificate.
func (c X509Certificate) MarshalYAML() (any, error) {
	return c.String(), nil
}

// UnmarshalJSON implements json.Unmarshaler for X509Certificate.
func (c *X509Certificate) UnmarshalJSON(b []byte) error {
	var certStr string
	err := json.Unmarshal(b, &certStr)
	if err != nil {
		return err
	}

	block, _ := pem.Decode([]byte(certStr))
	if block == nil {
		return fmt.Errorf("Failed to decode certificate")
	}

	cert, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		return err
	}

	*c = X509Certificate{Certificate: cert}

	return nil
}

// UnmarshalYAML implements yaml.Unmarshaler for X509Certificate.
func (c *X509Certificate) UnmarshalYAML(unmarshal func(v any) error) error {
	var certStr string
	err := unmarshal(&certStr)
	if err != nil {
		return err
	}

	block, _ := pem.Decode([]byte(certStr))
	if block == nil {
		return fmt.Errorf("Failed to decode certificate")
	}

	cert, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		return err
	}

	*c = X509Certificate{Certificate: cert}

	return nil
}
