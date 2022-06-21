package resources

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/lxc/lxd/lxd/db/query"
	"github.com/lxc/lxd/lxd/response"

	"github.com/canonical/microcluster/internal/db"
	"github.com/canonical/microcluster/internal/rest"
	"github.com/canonical/microcluster/internal/rest/access"
	"github.com/canonical/microcluster/internal/rest/types"
	"github.com/canonical/microcluster/internal/state"
)

var sqlCmd = rest.Endpoint{
	Path: "sql",

	Get:  rest.EndpointAction{Handler: sqlGet, AccessHandler: access.AllowAuthenticated},
	Post: rest.EndpointAction{Handler: sqlPost, AccessHandler: access.AllowAuthenticated},
}

// Perform a database dump.
func sqlGet(state *state.State, r *http.Request) response.Response {
	parentCtx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()

	schemaOnly, err := strconv.Atoi(r.FormValue("schema"))
	if err != nil {
		schemaOnly = 0
	}

	var dump string
	err = state.Database.Transaction(parentCtx, func(ctx context.Context, tx *db.Tx) error {
		dump, err = query.Dump(ctx, tx, schemaOnly == 1)
		if err != nil {
			return fmt.Errorf("failed dump database: %w", err)
		}

		return nil
	})
	if err != nil {
		return response.SmartError(err)
	}

	return response.SyncResponse(true, types.SQLDump{Text: dump})
}

// Execute queries.
func sqlPost(state *state.State, r *http.Request) response.Response {
	parentCtx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()
	req := &types.SQLQuery{}
	// Parse the request.
	err := json.NewDecoder(r.Body).Decode(&req)
	if err != nil {
		return response.BadRequest(err)
	}

	if req.Query == "" {
		return response.BadRequest(fmt.Errorf("No query provided"))
	}

	// TODO: Handle .sync query.

	batch := types.SQLBatch{}
	for _, query := range strings.Split(req.Query, ";") {
		query = strings.TrimLeft(query, " ")
		if query == "" {
			continue
		}

		result := types.SQLResult{}
		err = state.Database.Transaction(parentCtx, func(ctx context.Context, tx *db.Tx) error {
			if strings.HasPrefix(strings.ToUpper(query), "SELECT") {
				err = sqlSelect(ctx, tx, query, &result)
			} else {
				err = sqlExec(ctx, tx, query, &result)
			}

			return err
		})
		if err != nil {
			return response.SmartError(err)
		}

		batch.Results = append(batch.Results, result)
	}

	return response.SyncResponse(true, batch)
}

func sqlSelect(ctx context.Context, tx *sql.Tx, query string, result *types.SQLResult) error {
	result.Type = "select"
	rows, err := tx.QueryContext(ctx, query)
	if err != nil {
		return fmt.Errorf("Failed to execute query: %w", err)
	}

	defer rows.Close()

	result.Columns, err = rows.Columns()
	if err != nil {
		return fmt.Errorf("Failed to fetch colume names: %w", err)
	}

	for rows.Next() {
		row := make([]interface{}, len(result.Columns))
		rowPointers := make([]interface{}, len(result.Columns))
		for i := range row {
			rowPointers[i] = &row[i]
		}

		err := rows.Scan(rowPointers...)
		if err != nil {
			return fmt.Errorf("Failed to scan row: %w", err)
		}

		for i, column := range row {
			// Convert bytes to string. This is safe as
			// long as we don't have any BLOB column type.
			data, ok := column.([]byte)
			if ok {
				row[i] = string(data)
			}
		}

		result.Rows = append(result.Rows, row)
	}

	err = rows.Err()
	if err != nil {
		return fmt.Errorf("Got a row error: %w", err)
	}

	return nil
}

func sqlExec(ctx context.Context, tx *sql.Tx, query string, result *types.SQLResult) error {
	result.Type = "exec"
	r, err := tx.ExecContext(ctx, query)
	if err != nil {
		return fmt.Errorf("Failed to exec query: %w", err)
	}

	result.RowsAffected, err = r.RowsAffected()
	if err != nil {
		return fmt.Errorf("Failed to fetch affected rows: %w", err)
	}

	return nil
}
