package update

import (
	"context"
	"database/sql"
	"fmt"
	"testing"
	"time"

	"github.com/canonical/lxd/lxd/db/query"
	"github.com/canonical/lxd/lxd/db/schema"
	"github.com/stretchr/testify/suite"
)

type updateSuite struct {
	suite.Suite
}

func TestUpdateSuite(t *testing.T) {
	suite.Run(t, new(updateSuite))
}

// Ensures the internal_cluster_members table is properly updated by updateFromV1 if it already exists.
func (s *updateSuite) Test_updateFromV1ClusterMembers() {
	db, err := sql.Open("sqlite3", ":memory:")
	s.NoError(err)

	ctx := context.Background()
	tx, err := db.BeginTx(ctx, nil)
	s.NoError(err)

	// Run the v0 update first so we have a basic schema.
	s.NoError(updateFromV0(ctx, tx))
	s.NoError(tx.Commit())

	// Create 3 cluster members with the old schema versioning.
	createStmt := `INSERT INTO internal_cluster_members (name, address, certificate, schema, heartbeat, role) VALUES (?, ?, ?, ?, ?, ?)`
	for i := 0; i < 3; i++ {
		_, err := db.Exec(createStmt, fmt.Sprintf("member-%d", i), fmt.Sprintf("10.0.0.%d:8443", i), fmt.Sprintf("test-cert-%d", i), 3, time.Time{}, "voter")
		s.NoError(err)
	}

	// Update the schemas table to reflect the 3 new nodes. Assume there are 2 pre-existing external updates.
	stmt := `INSERT INTO schemas (version, updated_at) VALUES (?, strftime("%s"))`
	_, err = db.Exec(stmt, 1)
	_, err = db.Exec(stmt, 2)
	_, err = db.Exec(stmt, 3)
	s.NoError(err)

	// Create a schema manager that corresponds to the manual configuration above.
	dummyUpdate := func(ctx context.Context, tx *sql.Tx) error { return nil }
	schemaMgr := NewSchema()
	schemaMgr.AppendSchema([]schema.Update{dummyUpdate, dummyUpdate})

	// Apply the updates the regular way.
	_, err = schemaMgr.Schema().Ensure(db)
	s.NoError(err)

	tx, err = db.BeginTx(ctx, nil)
	s.NoError(err)
	schemaInternal, err := query.SelectIntegers(ctx, tx, "SELECT schema_internal FROM internal_cluster_members")
	s.NoError(err)

	schemaExternal, err := query.SelectIntegers(ctx, tx, "SELECT schema_external FROM internal_cluster_members")
	s.NoError(err)

	versionsInternal, err := query.SelectIntegers(ctx, tx, "SELECT version from schemas where type = 0")
	s.NoError(err)

	versionsExternal, err := query.SelectIntegers(ctx, tx, "SELECT version from schemas where type = 1")
	s.NoError(err)
	s.NoError(tx.Commit())

	// Ensure schema versions are split across internal and external updates in the internal_cluster_members table.
	s.Equal(3, len(schemaInternal))
	s.Equal(3, len(schemaExternal))

	// The schema_internal column won't be updated to 2 until `waitUpgrade` is called on each node, but it should still reflect the pre-existing updateFromV0.
	s.Equal([]int{1, 1, 1}, schemaInternal)
	s.Equal([]int{2, 2, 2}, schemaExternal)
	s.Equal([]int{1, 2}, versionsInternal)
	s.Equal([]int{1, 2}, versionsExternal)

	s.NoError(db.Close())
}

// Ensures the schema is properly split by the updateFromV1 function from various update patterns.
func (s *updateSuite) Test_updateFromV1() {
	dummyUpdate := func(ctx context.Context, tx *sql.Tx) error { return nil }

	tests := []struct {
		name                  string
		initialSchemaInternal []schema.Update
		initialSchemaExternal []schema.Update
		upgradesInternal      []schema.Update
		upgradesExternal      []schema.Update
	}{
		{
			name:                  "Default internal schema, no external schema, no updates",
			initialSchemaInternal: []schema.Update{updateFromV0, updateFromV1},
			initialSchemaExternal: []schema.Update{},
			upgradesInternal:      []schema.Update{},
			upgradesExternal:      []schema.Update{},
		},
		{
			name:                  "Upgrade internal schema from v0 to v1, no external schema",
			initialSchemaInternal: []schema.Update{updateFromV0},
			initialSchemaExternal: []schema.Update{},
			upgradesInternal:      []schema.Update{updateFromV1},
			upgradesExternal:      []schema.Update{},
		},
		{
			name:                  "Updating internal schema from v0 to v2, no external schema",
			initialSchemaInternal: []schema.Update{updateFromV0},
			initialSchemaExternal: []schema.Update{},
			upgradesInternal:      []schema.Update{updateFromV1, dummyUpdate},
			upgradesExternal:      []schema.Update{},
		},
		{
			name:                  "Updating internal schema from v1 to v2, no external schema",
			initialSchemaInternal: []schema.Update{updateFromV0, updateFromV1},
			initialSchemaExternal: []schema.Update{},
			upgradesInternal:      []schema.Update{dummyUpdate},
			upgradesExternal:      []schema.Update{},
		},
		{
			name:                  "Default internal schema, v1 external schema, no updates",
			initialSchemaInternal: []schema.Update{updateFromV0, updateFromV1},
			initialSchemaExternal: []schema.Update{dummyUpdate},
			upgradesInternal:      []schema.Update{},
			upgradesExternal:      []schema.Update{},
		},
		{
			name:                  "Default internal schema, update external schema from v0 to v1",
			initialSchemaInternal: []schema.Update{updateFromV0, updateFromV1},
			initialSchemaExternal: []schema.Update{},
			upgradesInternal:      []schema.Update{},
			upgradesExternal:      []schema.Update{dummyUpdate},
		},
		{
			name:                  "Default internal schema, update external schema from v1 to v2",
			initialSchemaInternal: []schema.Update{updateFromV0, updateFromV1},
			initialSchemaExternal: []schema.Update{dummyUpdate},
			upgradesInternal:      []schema.Update{},
			upgradesExternal:      []schema.Update{dummyUpdate},
		},
		{
			name:                  "Update internal schema from v1 to v2, update external schema from v1 to v2",
			initialSchemaInternal: []schema.Update{updateFromV0, updateFromV1},
			initialSchemaExternal: []schema.Update{dummyUpdate},
			upgradesInternal:      []schema.Update{dummyUpdate},
			upgradesExternal:      []schema.Update{dummyUpdate},
		},
		{
			name:                  "Update internal schema from v0 to v1, external schema at v1",
			initialSchemaInternal: []schema.Update{updateFromV0},
			initialSchemaExternal: []schema.Update{dummyUpdate},
			upgradesInternal:      []schema.Update{updateFromV1},
			upgradesExternal:      []schema.Update{},
		},
		{
			name:                  "Update internal schema from v0 to v2, external schema at v1",
			initialSchemaInternal: []schema.Update{updateFromV0},
			initialSchemaExternal: []schema.Update{dummyUpdate},
			upgradesInternal:      []schema.Update{updateFromV1, dummyUpdate},
			upgradesExternal:      []schema.Update{},
		},
		{
			name:                  "Update internal schema from v0 to v2, update external schema from v0 to v1",
			initialSchemaInternal: []schema.Update{updateFromV0},
			initialSchemaExternal: []schema.Update{},
			upgradesInternal:      []schema.Update{updateFromV1, dummyUpdate},
			upgradesExternal:      []schema.Update{dummyUpdate},
		},
		{
			name:                  "Update internal schema from v0 to v2, update external schema from v1 to v2",
			initialSchemaInternal: []schema.Update{updateFromV0},
			initialSchemaExternal: []schema.Update{dummyUpdate},
			upgradesInternal:      []schema.Update{updateFromV1, dummyUpdate},
			upgradesExternal:      []schema.Update{dummyUpdate},
		},
	}

	for i, t := range tests {
		s.T().Logf("%s (case %d)", t.name, i)

		schema := &SchemaUpdateManager{
			updates: map[updateType][]schema.Update{
				updateInternal: t.initialSchemaInternal,
				updateExternal: t.initialSchemaExternal,
			},
		}

		db, err := NewTestDBWithSchema(schema)
		s.NoError(err)

		schema.updates[updateInternal] = append(schema.updates[updateInternal], t.upgradesInternal...)
		schema.updates[updateExternal] = append(schema.updates[updateExternal], t.upgradesExternal...)

		_, err = schema.Schema().Ensure(db)
		s.NoError(err)

		ctx := context.Background()
		tx, err := db.BeginTx(ctx, nil)
		s.NoError(err)
		versions, err := query.SelectIntegers(ctx, tx, "SELECT MAX(version) FROM schemas WHERE type = 0 UNION ALL SELECT COALESCE(MAX(version), 0) FROM schemas WHERE type = 1")
		s.NoError(err)

		s.Equal(len(schema.updates[updateInternal]), versions[0])
		s.Equal(len(schema.updates[updateExternal]), versions[1])

		err = tx.Commit()
		s.NoError(err)

		err = db.Close()
		s.NoError(err)
	}
}

// NewTestDBWithSchema returns a sqlite DB set up with the given schema updates.
func NewTestDBWithSchema(schemaManager *SchemaUpdateManager) (*sql.DB, error) {
	db, err := sql.Open("sqlite3", ":memory:")
	if err != nil {
		return nil, err
	}

	schema := schemaManager.Schema()
	_, err = schema.Ensure(db)
	if err != nil {
		return nil, err
	}

	return db, nil
}
