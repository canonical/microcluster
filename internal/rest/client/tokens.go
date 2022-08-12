package client

import (
	"context"
	"strings"
	"time"

	"github.com/canonical/microcluster/internal/rest/types"
	"github.com/lxc/lxd/shared/api"
)

// RequestToken requests a join token with the given name.
func (c *Client) RequestToken(ctx context.Context, name string) (string, error) {
	endpoint := PublicEndpoint
	if strings.HasSuffix(c.url.String(), "control.socket") {
		endpoint = ControlEndpoint
	}

	queryCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()

	var token string
	tokenRecord := types.TokenRecord{Name: name}
	err := c.QueryStruct(queryCtx, "POST", endpoint, api.NewURL().Path("tokens"), tokenRecord, &token)

	return token, err
}

// DeleteTokenRecord deletes the toekn record.
func (c *Client) DeleteTokenRecord(ctx context.Context, name string) error {
	endpoint := InternalEndpoint
	if strings.HasSuffix(c.url.String(), "control.socket") {
		endpoint = ControlEndpoint
	}

	queryCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()

	err := c.QueryStruct(queryCtx, "DELETE", endpoint, api.NewURL().Path("tokens", name), nil, nil)

	return err
}

// GetTokenRecords returns the token records.
func (c *Client) GetTokenRecords(ctx context.Context) ([]types.TokenRecord, error) {
	endpoint := PublicEndpoint
	if strings.HasSuffix(c.url.String(), "control.socket") {
		endpoint = ControlEndpoint
	}

	queryCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()

	tokenRecords := []types.TokenRecord{}
	err := c.QueryStruct(queryCtx, "GET", endpoint, api.NewURL().Path("tokens"), nil, &tokenRecords)

	return tokenRecords, err
}
