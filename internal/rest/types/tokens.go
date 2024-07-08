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

// KeyPair holds a certificate together with its private key and optional CA.
type KeyPair struct {
	Cert string `json:"cert" yaml:"cert"`
	Key  string `json:"key" yaml:"key"`
	CA   string `json:"ca" yaml:"ca"`
}

// TokenResponse holds the information for connecting to a cluster by a node with a valid join token.
type TokenResponse struct {
	// ClusterCert is the public key used across the cluster.
	ClusterCert types.X509Certificate `json:"cluster_cert" yaml:"cluster_cert"`

	// ClusterKey is the private key used across the cluster.
	ClusterKey string `json:"cluster_key" yaml:"cluster_key"`

	// ClusterMembers is the full list of cluster members that are currently present and available in the cluster.
	// The joiner supplies this list to dqlite so that it can start its database.
	ClusterMembers []ClusterMemberLocal `json:"cluster_members" yaml:"cluster_members"`

	// ClusterAdditionalCerts is the full list of certificates added for additional listeners.
	ClusterAdditionalCerts map[string]KeyPair

	// TrustedMember contains the address of the existing cluster member
	// who was dqlite leader at the time that the joiner supplied its join token.
	//
	// The trusted member will have already recorded the joiner's information in
	// its local truststore, and thus will trust requests from the joiner prior to fully joining.
	TrustedMember ClusterMemberLocal `json:"trusted_member" yaml:"trusted_member"`
}

// Token holds the information that is presented to the joining node when requesting a token.
type Token struct {
	// Secret is the underlying secret string used to authenticate the token.
	Secret string `json:"secret" yaml:"secret"`

	// Fingerprint is the fingerprint of the cluster certificate,
	// so that the joiner can verify that the public key of the cluster matches this request.
	Fingerprint string `json:"fingerprint" yaml:"fingerprint"`

	// JoinAddresses is the list of addresses of the existing cluster members that the joiner may supply the token to.
	// Internally, the first system to accept the token will forward it to the dqlite leader.
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
