package client

import (
	"context"
	"time"

	"github.com/canonical/lxd/shared/api"

	"github.com/canonical/microcluster/v3/microcluster/types"
)

// GetSQL gets a SQL dump of the database.
func GetSQL(ctx context.Context, c types.Client, schema bool) (*types.SQLDump, error) {
	reqCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()

	dump := &types.SQLDump{}

	endpoint := api.NewURL().Path("sql")
	if schema {
		endpoint.WithQuery("schema", "1")
	}

	err := c.Query(reqCtx, "GET", types.ControlEndpoint, &endpoint.URL, nil, dump)
	if err != nil {
		return nil, err
	}

	return dump, nil
}

// PostSQL executes a SQL query against the database.
func PostSQL(ctx context.Context, c types.Client, query types.SQLQuery) (*types.SQLBatch, error) {
	reqCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()

	batch := &types.SQLBatch{}
	err := c.Query(reqCtx, "POST", types.ControlEndpoint, &api.NewURL().Path("sql").URL, query, batch)
	if err != nil {
		return nil, err
	}

	return batch, nil
}
