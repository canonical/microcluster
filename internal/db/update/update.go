package update

import (
	"context"
	"database/sql"
	"fmt"

	"github.com/canonical/lxd/lxd/db/schema"

	"github.com/canonical/microcluster/internal/extensions"
)

// CreateSchema is the default schema applied when bootstrapping the database.
const CreateSchema = `
CREATE TABLE schemas (
  id          INTEGER    PRIMARY  KEY    AUTOINCREMENT  NOT  NULL,
  version     INTEGER    NOT      NULL,
  updated_at  DATETIME   NOT      NULL,
  UNIQUE      (version)
);
`

// SchemaUpdateManager contains a map of schema update type to slice of schema.Update.
type SchemaUpdateManager struct {
	updates map[updateType][]schema.Update

	apiExtensions extensions.Extensions
}

// NewSchema returns a new SchemaUpdateManager containing microcluster schema updates.
func NewSchema() *SchemaUpdateManager {
	return &SchemaUpdateManager{
		updates: map[updateType][]schema.Update{
			updateInternal: {
				updateFromV0,
				updateFromV1,
				updateFromV2,
			},
		},
	}
}

// SetInternalUpdates replaces the set of internal schema updates.
func (s *SchemaUpdateManager) SetInternalUpdates(updates []schema.Update) {
	if s.updates == nil {
		s.updates = map[updateType][]schema.Update{}
	}

	s.updates[updateInternal] = updates
}

// SetExternalUpdates replaces the set of external schema updates.
func (s *SchemaUpdateManager) SetExternalUpdates(updates []schema.Update) {
	if s.updates == nil {
		s.updates = map[updateType][]schema.Update{}
	}

	s.updates[updateExternal] = updates
}

// Schema returns a SchemaUpdate from the SchemaUpdateManager config.
func (s *SchemaUpdateManager) Schema() *SchemaUpdate {
	schema := &SchemaUpdate{updates: s.updates}
	schema.Fresh("")
	return schema
}

// AppendSchema sets the given schema and API updates as the list of external extensions on the update manager.
func (s *SchemaUpdateManager) AppendSchema(schemaExtensions []schema.Update, apiExtensions extensions.Extensions) {
	s.updates[updateExternal] = schemaExtensions
	s.apiExtensions = apiExtensions
}

func updateFromV2(ctx context.Context, tx *sql.Tx) error {
	stmt := `
CREATE TABLE internal_cluster_members_new (
    id                      INTEGER   PRIMARY  KEY    AUTOINCREMENT  NOT  NULL,
    name                    TEXT      NOT      NULL,
    address                 TEXT      NOT      NULL,
    certificate             TEXT      NOT      NULL,
    schema_internal         INTEGER   NOT      NULL,
  	schema_external         INTEGER   NOT      NULL,
    heartbeat               DATETIME  NOT      NULL,
    role                    TEXT      NOT      NULL,
    api_extensions          TEXT      NOT      NULL DEFAULT '[]',
    UNIQUE(name),
    UNIQUE(certificate)
);

INSERT INTO internal_cluster_members_new (id, name, address, certificate, schema_internal, schema_external, heartbeat, role, api_extensions)
SELECT id, name, address, certificate, schema_internal, schema_external, heartbeat, role, '[]' FROM internal_cluster_members;

DROP TABLE internal_cluster_members;
ALTER TABLE internal_cluster_members_new RENAME TO internal_cluster_members;
`
	_, err := tx.ExecContext(ctx, stmt)
	return err
}

// updateFromV1 fixes a bug in the schemas table. Previously there was no way to tell when an update was internal, so the last external update would be re-run instead.
// To fix this, we introduce a `type` column to the schemas table. The first schema update will be considered internal, as that is what microcluster initially shipped with.
// All other schema versions will be considered external.
//
// Because this update affects the schema update mechanism, it must necessarily be run manually before the regular schema updates.
// So first it checks if the column exists already, and in such a case does nothing.
func updateFromV1(ctx context.Context, tx *sql.Tx) error {
	stmt := `
SELECT count(name)
FROM pragma_table_info('schemas')
WHERE name IN ('type');
`

	var count int
	err := tx.QueryRow(stmt).Scan(&count)
	if err != nil {
		return err
	}

	if count != 1 {
		stmt := `
CREATE TABLE schemas_new (
  id          INTEGER    PRIMARY  KEY    AUTOINCREMENT  NOT  NULL,
  version     INTEGER    NOT      NULL,
	type        INTEGER    NOT      NULL,
  updated_at  DATETIME   NOT      NULL,
  UNIQUE      (version,  type)
);

INSERT INTO schemas_new SELECT id,version,0,updated_at FROM schemas WHERE version = 1;
INSERT INTO schemas_new SELECT id,(version-1),1,updated_at FROM schemas WHERE version > 1;

DROP TABLE schemas;
ALTER TABLE schemas_new RENAME TO schemas;

CREATE TABLE internal_cluster_members_new (
  id                   INTEGER   PRIMARY  KEY    AUTOINCREMENT  NOT  NULL,
  name                 TEXT      NOT      NULL,
  address              TEXT      NOT      NULL,
  certificate          TEXT      NOT      NULL,
  schema_internal      INTEGER   NOT      NULL,
  schema_external      INTEGER   NOT      NULL,
  heartbeat            DATETIME  NOT      NULL,
  role                 TEXT      NOT      NULL,
  UNIQUE(name),
  UNIQUE(certificate)
);

INSERT INTO internal_cluster_members_new SELECT id,name,address,certificate,1,(schema-1),heartbeat,role FROM internal_cluster_members;
DROP TABLE internal_cluster_members;
ALTER TABLE internal_cluster_members_new RENAME TO internal_cluster_members;
`
		_, err := tx.ExecContext(ctx, stmt)
		return err
	}

	return nil
}

func updateFromV0(ctx context.Context, tx *sql.Tx) error {
	stmt := fmt.Sprintf(`
%s

CREATE TABLE internal_token_records (
  id           INTEGER         PRIMARY  KEY    AUTOINCREMENT  NOT  NULL,
  name         TEXT            NOT      NULL,
  secret       TEXT            NOT      NULL,
  UNIQUE       (name),
  UNIQUE       (secret)
);

CREATE TABLE internal_cluster_members (
  id                   INTEGER   PRIMARY  KEY    AUTOINCREMENT  NOT  NULL,
  name                 TEXT      NOT      NULL,
  address              TEXT      NOT      NULL,
  certificate          TEXT      NOT      NULL,
  schema               INTEGER   NOT      NULL,
  heartbeat            DATETIME  NOT      NULL,
  role                 TEXT      NOT      NULL,
  UNIQUE(name),
  UNIQUE(certificate)
);
`, CreateSchema)

	_, err := tx.ExecContext(ctx, stmt)
	return err
}
