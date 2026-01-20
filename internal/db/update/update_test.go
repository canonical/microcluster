package update

import (
	"context"
	"database/sql"
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/suite"

	clusterDB "github.com/canonical/microcluster/v3/microcluster/db"
)

type updateSuite struct {
	suite.Suite
}

func TestUpdateSuite(t *testing.T) {
	suite.Run(t, new(updateSuite))
}

// Ensures the core_cluster_members table is properly updated by updateFromV1 if it already exists.
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
	s.NoError(err)
	_, err = db.Exec(stmt, 2)
	s.NoError(err)
	_, err = db.Exec(stmt, 3)
	s.NoError(err)

	// Create a schema manager that corresponds to the manual configuration above.
	dummyUpdate := func(ctx context.Context, tx *sql.Tx) error { return nil }
	schemaMgr := NewSchema()
	schemaMgr.AppendSchema([]clusterDB.Update{dummyUpdate, dummyUpdate}, nil)

	// Apply the updates the regular way.
	_, err = schemaMgr.Schema().Ensure(context.TODO(), db)
	s.NoError(err)

	tx, err = db.BeginTx(ctx, nil)
	s.NoError(err)
	schemaInternal, err := clusterDB.SelectIntegers(ctx, tx, "SELECT schema_internal FROM core_cluster_members")
	s.NoError(err)

	schemaExternal, err := clusterDB.SelectIntegers(ctx, tx, "SELECT schema_external FROM core_cluster_members")
	s.NoError(err)

	versionsInternal, err := clusterDB.SelectIntegers(ctx, tx, "SELECT version from schemas where type = 0")
	s.NoError(err)

	versionsExternal, err := clusterDB.SelectIntegers(ctx, tx, "SELECT version from schemas where type = 1")
	s.NoError(err)
	s.NoError(tx.Commit())

	// Ensure schema versions are split across internal and external updates in the core_cluster_members table.
	s.Equal(3, len(schemaInternal))
	s.Equal(3, len(schemaExternal))

	// The schema_internal column won't be updated to 2 until `waitUpgrade` is called on each node, but it should still reflect the pre-existing updateFromV0.
	s.Equal([]int{1, 1, 1}, schemaInternal)
	s.Equal([]int{2, 2, 2}, schemaExternal)

	internalUpdates := NewSchema().updates[updateInternal]
	expectedUpdates := make([]int, len(internalUpdates))
	for i := range internalUpdates {
		expectedUpdates[i] = i + 1
	}

	s.Equal(expectedUpdates, versionsInternal)
	s.Equal([]int{1, 2}, versionsExternal)

	s.NoError(db.Close())
}

// Ensures updateFromV6 moves roles to core_cluster_member_roles and drops the role column.
func (s *updateSuite) Test_updateFromV6ClusterMemberRoles() {
	db, err := sql.Open("sqlite3", ":memory:")
	s.NoError(err)

	ctx := context.Background()
	mgr := NewSchema()
	mgr.SetInternalUpdates([]clusterDB.Update{
		updateFromV0,
		updateFromV1,
		updateFromV2,
		mgr.updateFromV3,
		updateFromV4,
		updateFromV5,
	})
	mgr.SetExternalUpdates([]clusterDB.Update{})

	_, err = mgr.Schema().Ensure(ctx, db)
	s.NoError(err)

	_, err = db.Exec(`INSERT INTO core_cluster_members (name, address, certificate, schema_internal, schema_external, api_extensions, heartbeat, role)
VALUES (?, ?, ?, ?, ?, ?, ?, ?)`, "member-1", "10.0.0.1:8443", "cert-1", 1, 1, "[]", time.Time{}, "voter")
	s.NoError(err)

	tx, err := db.BeginTx(ctx, nil)
	s.NoError(err)
	s.NoError(updateFromV6(ctx, tx))
	s.NoError(tx.Commit())

	var count int
	err = db.QueryRow("SELECT count(name) FROM pragma_table_info('core_cluster_members') WHERE name = 'role'").Scan(&count)
	s.NoError(err)
	s.Equal(0, count)

	var role string
	var controlPlane int
	err = db.QueryRow("SELECT control_plane, dqlite_role FROM core_cluster_member_roles").Scan(&controlPlane, &role)
	s.NoError(err)
	s.Equal(0, controlPlane)
	s.Equal("voter", role)

	s.NoError(db.Close())
}

// Ensures the schema is properly split by the updateFromV1 function from various update patterns.
func (s *updateSuite) Test_updateFromV1() {
	dummyUpdate := func(ctx context.Context, tx *sql.Tx) error { return nil }

	tests := []struct {
		name                  string
		initialSchemaInternal []clusterDB.Update
		initialSchemaExternal []clusterDB.Update
		upgradesInternal      []clusterDB.Update
		upgradesExternal      []clusterDB.Update
	}{
		{
			name:                  "Default internal schema, no external schema, no updates",
			initialSchemaInternal: []clusterDB.Update{updateFromV0, updateFromV1},
			initialSchemaExternal: []clusterDB.Update{},
			upgradesInternal:      []clusterDB.Update{},
			upgradesExternal:      []clusterDB.Update{},
		},
		{
			name:                  "Upgrade internal schema from v0 to v1, no external schema",
			initialSchemaInternal: []clusterDB.Update{updateFromV0},
			initialSchemaExternal: []clusterDB.Update{},
			upgradesInternal:      []clusterDB.Update{updateFromV1},
			upgradesExternal:      []clusterDB.Update{},
		},
		{
			name:                  "Updating internal schema from v0 to v2, no external schema",
			initialSchemaInternal: []clusterDB.Update{updateFromV0},
			initialSchemaExternal: []clusterDB.Update{},
			upgradesInternal:      []clusterDB.Update{updateFromV1, dummyUpdate},
			upgradesExternal:      []clusterDB.Update{},
		},
		{
			name:                  "Updating internal schema from v1 to v2, no external schema",
			initialSchemaInternal: []clusterDB.Update{updateFromV0, updateFromV1},
			initialSchemaExternal: []clusterDB.Update{},
			upgradesInternal:      []clusterDB.Update{dummyUpdate},
			upgradesExternal:      []clusterDB.Update{},
		},
		{
			name:                  "Default internal schema, v1 external schema, no updates",
			initialSchemaInternal: []clusterDB.Update{updateFromV0, updateFromV1},
			initialSchemaExternal: []clusterDB.Update{dummyUpdate},
			upgradesInternal:      []clusterDB.Update{},
			upgradesExternal:      []clusterDB.Update{},
		},
		{
			name:                  "Default internal schema, update external schema from v0 to v1",
			initialSchemaInternal: []clusterDB.Update{updateFromV0, updateFromV1},
			initialSchemaExternal: []clusterDB.Update{},
			upgradesInternal:      []clusterDB.Update{},
			upgradesExternal:      []clusterDB.Update{dummyUpdate},
		},
		{
			name:                  "Default internal schema, update external schema from v1 to v2",
			initialSchemaInternal: []clusterDB.Update{updateFromV0, updateFromV1},
			initialSchemaExternal: []clusterDB.Update{dummyUpdate},
			upgradesInternal:      []clusterDB.Update{},
			upgradesExternal:      []clusterDB.Update{dummyUpdate},
		},
		{
			name:                  "Update internal schema from v1 to v2, update external schema from v1 to v2",
			initialSchemaInternal: []clusterDB.Update{updateFromV0, updateFromV1},
			initialSchemaExternal: []clusterDB.Update{dummyUpdate},
			upgradesInternal:      []clusterDB.Update{dummyUpdate},
			upgradesExternal:      []clusterDB.Update{dummyUpdate},
		},
		{
			name:                  "Update internal schema from v0 to v1, external schema at v1",
			initialSchemaInternal: []clusterDB.Update{updateFromV0},
			initialSchemaExternal: []clusterDB.Update{dummyUpdate},
			upgradesInternal:      []clusterDB.Update{updateFromV1},
			upgradesExternal:      []clusterDB.Update{},
		},
		{
			name:                  "Update internal schema from v0 to v2, external schema at v1",
			initialSchemaInternal: []clusterDB.Update{updateFromV0},
			initialSchemaExternal: []clusterDB.Update{dummyUpdate},
			upgradesInternal:      []clusterDB.Update{updateFromV1, dummyUpdate},
			upgradesExternal:      []clusterDB.Update{},
		},
		{
			name:                  "Update internal schema from v0 to v2, update external schema from v0 to v1",
			initialSchemaInternal: []clusterDB.Update{updateFromV0},
			initialSchemaExternal: []clusterDB.Update{},
			upgradesInternal:      []clusterDB.Update{updateFromV1, dummyUpdate},
			upgradesExternal:      []clusterDB.Update{dummyUpdate},
		},
		{
			name:                  "Update internal schema from v0 to v2, update external schema from v1 to v2",
			initialSchemaInternal: []clusterDB.Update{updateFromV0},
			initialSchemaExternal: []clusterDB.Update{dummyUpdate},
			upgradesInternal:      []clusterDB.Update{updateFromV1, dummyUpdate},
			upgradesExternal:      []clusterDB.Update{dummyUpdate},
		},
	}

	for i, t := range tests {
		s.T().Logf("%s (case %d)", t.name, i)

		schema := &SchemaUpdateManager{
			updates: map[updateType][]clusterDB.Update{
				updateInternal: t.initialSchemaInternal,
				updateExternal: t.initialSchemaExternal,
			},
		}

		db, err := NewTestDBWithSchema(schema)
		s.NoError(err)

		schema.updates[updateInternal] = append(schema.updates[updateInternal], t.upgradesInternal...)
		schema.updates[updateExternal] = append(schema.updates[updateExternal], t.upgradesExternal...)

		_, err = schema.Schema().Ensure(context.TODO(), db)
		s.NoError(err)

		ctx := context.Background()
		tx, err := db.BeginTx(ctx, nil)
		s.NoError(err)
		versions, err := clusterDB.SelectIntegers(ctx, tx, "SELECT MAX(version) FROM schemas WHERE type = 0 UNION ALL SELECT COALESCE(MAX(version), 0) FROM schemas WHERE type = 1")
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
	_, err = schema.Ensure(context.TODO(), db)
	if err != nil {
		return nil, err
	}

	return db, nil
}
