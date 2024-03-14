package types

import (
	"encoding/base64"
	"encoding/json"

	"github.com/canonical/microcluster/rest/types"
)

// TokenRecord holds information for requesting a join token.
//
// swagger:model
type TokenRecord struct {
	// Name of the server.
	// Example: server01
	Name string `json:"name" yaml:"name"`

	// Token to use by the server.
	// Example: encoded token string
	Token string `json:"token" yaml:"token"`
}

// TokenResponse holds the information for connecting to a cluster by a node with a valid join token.
//
// swagger:model
type TokenResponse struct {
	// Public key of the cluster.
	// Example: X509 public key
	ClusterCert types.X509Certificate `json:"cluster_cert" yaml:"cluster_cert"`

	// Private key of the cluster.
	// Example: X509 private key
	ClusterKey string `json:"cluster_key" yaml:"cluster_key"`

	// List of existing cluster members.
	// Example: List of ClusterMemberLocal
	ClusterMembers []ClusterMemberLocal `json:"cluster_members" yaml:"cluster_members"`
}

// Token holds the information that is presented to the joining node when requesting a token.
type Token struct {
	Name          string           `json:"name" yaml:"name"`
	Secret        string           `json:"secret" yaml:"secret"`
	Fingerprint   string           `json:"fingerprint" yaml:"fingerprint"`
	JoinAddresses []types.AddrPort `json:"join_addresses" yaml:"join_addresses"`
}

func (t Token) String() (string, error) {
	tokenData, err := json.Marshal(t)
	if err != nil {
		return "", err
	}

	return base64.StdEncoding.EncodeToString(tokenData), nil
}

// DecodeToken decodes a base64-encoded token string.
func DecodeToken(tokenString string) (*Token, error) {
	tokenData, err := base64.StdEncoding.DecodeString(tokenString)
	if err != nil {
		return nil, err
	}

	var token Token
	err = json.Unmarshal(tokenData, &token)
	if err != nil {
		return nil, err
	}

	return &token, nil
}
