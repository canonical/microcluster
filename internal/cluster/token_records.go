package cluster

import (
	"context"
	"crypto/x509"
	"database/sql"
	"log/slog"
	"time"

	"github.com/canonical/lxd/shared"

	"github.com/canonical/microcluster/v3/internal/log"
	"github.com/canonical/microcluster/v3/microcluster/types"
)

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
func (t *CoreTokenRecord) ToAPI(clusterCert *x509.Certificate, joinAddresses []types.AddrPort) (*types.TokenRecord, error) {
	token := types.Token{
		Secret:        t.Secret,
		Fingerprint:   shared.CertFingerprint(clusterCert),
		JoinAddresses: joinAddresses,
	}

	tokenString, err := token.String()
	if err != nil {
		return nil, err
	}

	return &types.TokenRecord{
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

	logger, err := log.LoggerFromContext(ctx)
	if err != nil {
		return err
	}

	for _, token := range tokens {
		if token.Expired() {
			err = DeleteCoreTokenRecord(ctx, tx, token.Name)
			if err != nil {
				return err
			}

			logger.Info("Removed expired join token", slog.String("name", token.Name))
		}
	}

	return nil
}
