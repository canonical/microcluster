// Package database provides the database access functions and schema.
package database

import (
	"context"
	"database/sql"

	"github.com/lxc/lxd/lxd/db/schema"
)

// SchemaExtensions is a list of schema extensions that can be passed to the MicroCluster daemon.
// Each entry will increase the database schema version by one, and will be applied after internal schema updates.
var SchemaExtensions = map[int]schema.Update{
	1: schemaAppend1,
	2: schemaAppend2,
}

func schemaAppend1(ctx context.Context, tx *sql.Tx) error {
	stmt := `
CREATE TABLE extended_table (
  id           INTEGER  PRIMARY         KEY    AUTOINCREMENT  NOT  NULL,
  key          TEXT     NOT             NULL,
  value        TEXT     NOT             NULL,
  UNIQUE(key)
);
  `

	_, err := tx.ExecContext(ctx, stmt)

	return err
}

func schemaAppend2(ctx context.Context, tx *sql.Tx) error {
	stmt := `
CREATE TABLE some_other_table (
  id                  INTEGER  PRIMARY           KEY    AUTOINCREMENT  NOT  NULL,
  field_one           TEXT     NOT               NULL,
  field_two           TEXT     NOT               NULL,
  UNIQUE(field_one),
  UNIQUE(field_two)
);
  `

	_, err := tx.ExecContext(ctx, stmt)

	return err
}
