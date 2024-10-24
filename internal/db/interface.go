package db

import (
	"context"
	"database/sql"

	dqliteClient "github.com/canonical/go-dqlite/v3/client"

	"github.com/canonical/microcluster/v3/internal/extensions"
	"github.com/canonical/microcluster/v3/rest/types"
)

// DB exposes the internal database for use with external projects.
type DB interface {
	// Transaction handles performing a transaction on the dqlite database.
	Transaction(outerCtx context.Context, f func(context.Context, *sql.Tx) error) error

	// Leader returns a client connected to the leader of the dqlite cluster.
	Leader(ctx context.Context) (*dqliteClient.Client, error)

	// Cluster returns information about dqlite cluster members.
	Cluster(ctx context.Context, client *dqliteClient.Client) ([]dqliteClient.NodeInfo, error)

	// Status returns the current status of the database.
	Status() types.DatabaseStatus

	// IsOpen returns nil  only if the DB has been opened and the schema loaded.
	// Otherwise, it returns an error describing why the database is offline.
	// The returned error may have the http status 503, indicating that the database is in a valid but unavailable state.
	IsOpen(ctx context.Context) error

	// SchemaVersion returns the current internal and external schema version, as well as all API extensions in memory.
	SchemaVersion() (versionInternal uint64, versionExternal uint64, apiExtensions extensions.Extensions)
}
