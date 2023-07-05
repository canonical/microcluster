package cluster

import (
	"crypto/x509"

	"github.com/canonical/lxd/shared"
	internalTypes "github.com/canonical/microcluster/internal/rest/types"
	"github.com/canonical/microcluster/rest/types"
)

// Code generation directives.
//
//go:generate -command mapper lxd-generate db mapper -t token_records.mapper.go
//go:generate mapper reset
//
//go:generate mapper stmt -e internal_token_record objects table=internal_token_records
//go:generate mapper stmt -e internal_token_record objects-by-Secret table=internal_token_records
//go:generate mapper stmt -e internal_token_record id table=internal_token_records
//go:generate mapper stmt -e internal_token_record create table=internal_token_records
//go:generate mapper stmt -e internal_token_record delete-by-Name table=internal_token_records
//
//go:generate mapper method -e internal_token_record ID table=internal_token_records
//go:generate mapper method -e internal_token_record Exists table=internal_token_records
//go:generate mapper method -e internal_token_record GetOne table=internal_token_records
//go:generate mapper method -e internal_token_record GetMany table=internal_token_records
//go:generate mapper method -e internal_token_record Create table=internal_token_records
//go:generate mapper method -e internal_token_record DeleteOne-by-Name table=internal_token_records

// InternalTokenRecord is the database representation of a join token record.
type InternalTokenRecord struct {
	ID     int
	Secret string `db:"primary=yes"`
	Name   string
}

// InternalTokenRecordFilter is the filter struct for filtering results from generated methods.
type InternalTokenRecordFilter struct {
	ID     *int
	Secret *string
	Name   *string
}

// ToAPI converts the InternalTokenRecord to a full token and returns an API compatible struct.
func (t *InternalTokenRecord) ToAPI(clusterCert *x509.Certificate, joinAddresses []types.AddrPort) (*internalTypes.TokenRecord, error) {
	token := internalTypes.Token{
		Secret:        t.Secret,
		Fingerprint:   shared.CertFingerprint(clusterCert),
		JoinAddresses: joinAddresses,
	}

	tokenString, err := token.String()
	if err != nil {
		return nil, err
	}

	return &internalTypes.TokenRecord{
		Token: tokenString,
		Name:  t.Name,
	}, nil
}
