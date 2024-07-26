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
	mgr := &SchemaUpdateManager{}
	mgr.updates = map[updateType][]schema.Update{
		updateInternal: {
			updateFromV0,
			updateFromV1,
			updateFromV2,
			mgr.updateFromV3,
			updateFromV4,
			updateFromV5,
		},
	}

	return mgr
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
	schema := &SchemaUpdate{updates: s.updates, apiExtensions: s.apiExtensions}
	schema.Fresh("")
	return schema
}

// AppendSchema sets the given schema and API updates as the list of external extensions on the update manager.
func (s *SchemaUpdateManager) AppendSchema(schemaExtensions []schema.Update, apiExtensions extensions.Extensions) {
	s.updates[updateExternal] = schemaExtensions
	s.apiExtensions = apiExtensions
}

// updateFromV5 adds an expiration column for join tokens.
func updateFromV5(ctx context.Context, tx *sql.Tx) error {
	stmt := `CREATE TABLE core_token_records_new (
  id           INTEGER         PRIMARY  KEY    AUTOINCREMENT  NOT  NULL,
  name         TEXT            NOT      NULL,
  secret       TEXT            NOT      NULL,
	expiry_date  DATETIME,
  UNIQUE       (name),
  UNIQUE       (secret)
);

INSERT INTO core_token_records_new (id, name, secret, expiry_date) SELECT id,name,secret,NULL FROM core_token_records;
DROP TABLE core_token_records;
ALTER TABLE core_token_records_new RENAME TO core_token_records;
`

	_, err := tx.ExecContext(ctx, stmt)

	return err
}

// updateFromV4 renames the internal_ prefixed tables to core_ to signify that
// they are accessible outside of the internal microcluster implementation.
func updateFromV4(ctx context.Context, tx *sql.Tx) error {
	stmt := `
ALTER TABLE internal_cluster_members RENAME TO core_cluster_members;
ALTER TABLE internal_token_records RENAME TO core_token_records;
	`

	_, err := tx.ExecContext(ctx, stmt)

	return err
}

// updateFromV3 auto-applies the initial set of API extensions to the core_cluster_members table.
// This is done so that the cluster won't have to be notified twice,
// once for the schema update that introduces API extensions to be applied,
// and another time for API extensions to be applied.
func (s *SchemaUpdateManager) updateFromV3(ctx context.Context, tx *sql.Tx) error {
	stmt := "UPDATE internal_cluster_members SET api_extensions=?"
	_, err := tx.ExecContext(ctx, stmt, s.apiExtensions)

	return err
}

// updateFromV2 introduces API extensions as a column on the internal_cluster_members table.
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

CREATE TABLE IF NOT EXISTS internal_cluster_members_new (
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
`
		_, err = tx.ExecContext(ctx, stmt)
		if err != nil {
			return err
		}

		// Check if we have pre-emptively created this table when checking for schema updates. If so, we won't need to insert any rows.
		stmt = `SELECT count(id) from internal_cluster_members_new`
		err := tx.QueryRow(stmt).Scan(&count)
		if err != nil {
			return err
		}

		stmt = ""
		if count == 0 {
			stmt = "INSERT INTO internal_cluster_members_new SELECT id,name,address,certificate,1,(schema-1),heartbeat,role FROM internal_cluster_members;"
		}

		stmt = fmt.Sprintf(`%s
DROP TABLE internal_cluster_members;
ALTER TABLE internal_cluster_members_new RENAME TO internal_cluster_members;
DROP TRIGGER IF EXISTS internal_cluster_member_added;
DROP TRIGGER IF EXISTS internal_cluster_member_removed;
`, stmt)
		_, err = tx.ExecContext(ctx, stmt)
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
