package client

import (
	"context"
	"time"

	"github.com/canonical/lxd/shared/api"

	"github.com/canonical/microcluster/internal/rest/types"
)

// GetSQL gets a SQL dump of the database.
func (c *Client) GetSQL(ctx context.Context, schema bool) (*types.SQLDump, error) {
	reqCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()

	dump := &types.SQLDump{}

	endpoint := api.NewURL().Path("sql")
	if schema {
		endpoint.WithQuery("schema", "1")
	}

	err := c.QueryStruct(reqCtx, "GET", InternalEndpoint, endpoint, nil, dump)
	if err != nil {
		return nil, err
	}

	return dump, nil
}

// PostSQL executes a SQL query against the database.
func (c *Client) PostSQL(ctx context.Context, query types.SQLQuery) (*types.SQLBatch, error) {
	reqCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()

	batch := &types.SQLBatch{}
	err := c.QueryStruct(reqCtx, "POST", InternalEndpoint, api.NewURL().Path("sql"), query, batch)
	if err != nil {
		return nil, err
	}

	return batch, nil
}
