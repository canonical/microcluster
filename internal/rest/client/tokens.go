package client

import (
	"context"
	"strings"
	"time"

	"github.com/canonical/microcluster/internal/rest/types"
	"github.com/lxc/lxd/shared/api"
)

// RequestToken requests a join token with the given certificate fingerprint.
func (c *Client) RequestToken(ctx context.Context, fingerprint string) (string, error) {
	endpoint := PublicEndpoint
	if strings.HasSuffix(c.url.String(), "control.socket") {
		endpoint = ControlEndpoint
	}

	queryCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()

	var token string
	tokenRecord := types.TokenRecord{JoinerCert: fingerprint}
	err := c.QueryStruct(queryCtx, "POST", endpoint, api.NewURL().Path("tokens"), tokenRecord, &token)

	return token, err
}

// SubmitToken authenticates a token and returns information necessary to join the cluster.
func (c *Client) SubmitToken(ctx context.Context, fingerprint string, token string) (types.TokenResponse, error) {
	queryCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()

	var tokenResponse types.TokenResponse
	tokenRecord := types.TokenRecord{Token: token}
	err := c.QueryStruct(queryCtx, "POST", InternalEndpoint, api.NewURL().Path("tokens", fingerprint), tokenRecord, &tokenResponse)

	return tokenResponse, err
}

// DeleteTokenRecord deletes the toekn record.
func (c *Client) DeleteTokenRecord(ctx context.Context, fingerprint string) error {
	endpoint := InternalEndpoint
	if strings.HasSuffix(c.url.String(), "control.socket") {
		endpoint = ControlEndpoint
	}

	queryCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()

	err := c.QueryStruct(queryCtx, "DELETE", endpoint, api.NewURL().Path("tokens", fingerprint), nil, nil)

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
