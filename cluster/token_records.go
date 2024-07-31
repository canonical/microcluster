package cluster

import (
	"context"
	"crypto/x509"
	"database/sql"
	"time"

	"github.com/canonical/lxd/shared"
	"github.com/canonical/lxd/shared/logger"

	internalTypes "github.com/canonical/microcluster/v2/internal/rest/types"
	"github.com/canonical/microcluster/v2/rest/types"
)

// Code generation directives.
//
//go:generate -command mapper lxd-generate db mapper -t token_records.mapper.go
//go:generate mapper reset
//
//go:generate mapper stmt -e core_token_record objects table=core_token_records
//go:generate mapper stmt -e core_token_record objects-by-Secret table=core_token_records
//go:generate mapper stmt -e core_token_record id table=core_token_records
//go:generate mapper stmt -e core_token_record create table=core_token_records
//go:generate mapper stmt -e core_token_record delete-by-Name table=core_token_records
//
//go:generate mapper method -e core_token_record ID table=core_token_records
//go:generate mapper method -e core_token_record Exists table=core_token_records
//go:generate mapper method -e core_token_record GetOne table=core_token_records
//go:generate mapper method -e core_token_record GetMany table=core_token_records
//go:generate mapper method -e core_token_record Create table=core_token_records
//go:generate mapper method -e core_token_record DeleteOne-by-Name table=core_token_records

// CoreTokenRecord is the database representation of a join token record.
type CoreTokenRecord struct {
	ID         int
	Secret     string `db:"primary=yes"`
	Name       string
	ExpiryDate sql.NullTime
}

// CoreTokenRecordFilter is the filter struct for filtering results from generated methods.
type CoreTokenRecordFilter struct {
	ID     *int
	Secret *string
	Name   *string
}

// ToAPI converts the CoreTokenRecord to a full token and returns an API compatible struct.
func (t *CoreTokenRecord) ToAPI(clusterCert *x509.Certificate, joinAddresses []types.AddrPort) (*internalTypes.TokenRecord, error) {
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
		Token:     tokenString,
		Name:      t.Name,
		ExpiresAt: t.ExpiryDate.Time,
	}, nil
}

// Expired compares the token's expiry date with the current time.
func (t *CoreTokenRecord) Expired() bool {
	return t.ExpiryDate.Valid && t.ExpiryDate.Time.Before(time.Now())
}

// DeleteExpiredCoreTokenRecords cleans up expired tokens.
func DeleteExpiredCoreTokenRecords(ctx context.Context, tx *sql.Tx) error {
	tokens, err := GetCoreTokenRecords(ctx, tx)
	if err != nil {
		return err
	}

	for _, token := range tokens {
		if token.Expired() {
			err = DeleteCoreTokenRecord(ctx, tx, token.Name)
			if err != nil {
				return err
			}

			logger.Info("Removed expired join token", logger.Ctx{"name": token.Name})
		}
	}

	return nil
}
