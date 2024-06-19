package client

import (
	"context"
	"time"

	"github.com/canonical/lxd/shared/api"

	"github.com/canonical/microcluster/internal/rest/types"
)

// RequestToken requests a join token with the given name.
func (c *Client) RequestToken(ctx context.Context, name string) (string, error) {
	queryCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()

	var token string
	tokenRecord := types.TokenRecord{Name: name}
	err := c.QueryStruct(queryCtx, "POST", types.PublicEndpoint, api.NewURL().Path("tokens"), tokenRecord, &token)

	return token, err
}

// DeleteTokenRecord deletes the toekn record.
func (c *Client) DeleteTokenRecord(ctx context.Context, name string) error {
	queryCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()

	err := c.QueryStruct(queryCtx, "DELETE", types.InternalEndpoint, api.NewURL().Path("tokens", name), nil, nil)

	return err
}

// GetTokenRecords returns the token records.
func (c *Client) GetTokenRecords(ctx context.Context) ([]types.TokenRecord, error) {
	queryCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()

	tokenRecords := []types.TokenRecord{}
	err := c.QueryStruct(queryCtx, "GET", types.PublicEndpoint, api.NewURL().Path("tokens"), nil, &tokenRecords)

	return tokenRecords, err
}
