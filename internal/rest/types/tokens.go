package types

import (
	"encoding/base64"
	"encoding/json"

	"github.com/canonical/microcluster/rest/types"
)

// TokenRecord holds information for requesting a join token.
type TokenRecord struct {
	Name  string `json:"name" yaml:"name"`
	Token string `json:"token" yaml:"token"`
}

// TokenResponse holds the information for connecting to a cluster by a node with a valid join token.
type TokenResponse struct {
	ClusterCert    types.X509Certificate `json:"cluster_cert" yaml:"cluster_cert"`
	ClusterKey     string                `json:"cluster_key" yaml:"cluster_key"`
	ClusterMembers []ClusterMemberLocal  `json:"cluster_members" yaml:"cluster_members"`
}

// Token holds the information that is presented to the joining node when requesting a token.
type Token struct {
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
