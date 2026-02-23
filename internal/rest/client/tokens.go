package client

import (
	"context"
	"time"

	"github.com/canonical/lxd/shared/api"

	"github.com/canonical/microcluster/v3/microcluster/types"
)

// RequestToken requests a join token with the given name.
func RequestToken(ctx context.Context, c types.Client, name string, expireAfter time.Duration) (string, error) {
	queryCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()

	var token string
	tokenRecord := types.TokenRequest{Name: name, ExpireAfter: expireAfter}
	err := c.Query(queryCtx, "POST", types.ControlEndpoint, &api.NewURL().Path("tokens").URL, tokenRecord, &token)

	return token, err
}

// DeleteTokenRecord deletes the token record.
func DeleteTokenRecord(ctx context.Context, c types.Client, name string) error {
	queryCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()

	err := c.Query(queryCtx, "DELETE", types.ControlEndpoint, &api.NewURL().Path("tokens", name).URL, nil, nil)

	return err
}

// GetTokenRecords returns the token records.
func GetTokenRecords(ctx context.Context, c types.Client) ([]types.TokenRecord, error) {
	queryCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()

	tokenRecords := []types.TokenRecord{}
	err := c.Query(queryCtx, "GET", types.ControlEndpoint, &api.NewURL().Path("tokens").URL, nil, &tokenRecords)

	return tokenRecords, err
}
