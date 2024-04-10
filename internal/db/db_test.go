package db

import (
	"context"
	"database/sql"
	"fmt"
	"testing"
	"time"

	"github.com/canonical/lxd/lxd/db/query"
	"github.com/canonical/lxd/lxd/db/schema"
	"github.com/canonical/lxd/shared/api"
	"github.com/stretchr/testify/suite"

	"github.com/canonical/microcluster/cluster"
	"github.com/canonical/microcluster/internal/db/update"
)

type dbSuite struct {
	suite.Suite
}

func TestDBSuite(t *testing.T) {
	suite.Run(t, new(dbSuite))
}

// Ensures waitUpgrade properly waits or returns an error with mismatched cluster versions.
func (s *dbSuite) Test_waitUpgrade() {
	type versions struct {
		schemaInt uint64
		schemaExt uint64
	}

	tests := []struct {
		name              string
		upgradedLocalInfo versions
		clusterMembers    []versions
		expectErr         error
		expectWait        bool
	}{
		{
			name:              "No upgrade, no other nodes",
			upgradedLocalInfo: versions{schemaInt: 0, schemaExt: 0},
		},
		{
			name:              "Internal upgrade, no other nodes",
			upgradedLocalInfo: versions{schemaInt: 1, schemaExt: 0},
		},
		{
			name:              "External upgrade, no other nodes",
			upgradedLocalInfo: versions{schemaInt: 0, schemaExt: 1},
		},
		{
			name:              "Full upgrade, no other nodes",
			upgradedLocalInfo: versions{schemaInt: 1, schemaExt: 1},
		},
		{
			name:              "All other nodes ahead (internal only)",
			upgradedLocalInfo: versions{schemaInt: 0, schemaExt: 0},
			clusterMembers:    []versions{{schemaInt: 1, schemaExt: 0}, {schemaInt: 1, schemaExt: 0}},
			expectErr:         fmt.Errorf("This node's version is behind, please upgrade"),
		},
		{
			name:              "All other nodes ahead (external only)",
			upgradedLocalInfo: versions{schemaInt: 0, schemaExt: 0},
			clusterMembers:    []versions{{schemaInt: 0, schemaExt: 1}, {schemaInt: 0, schemaExt: 1}},
			expectErr:         fmt.Errorf("This node's version is behind, please upgrade"),
		},
		{
			name:              "All other nodes ahead (internal/external mismatch)",
			upgradedLocalInfo: versions{schemaInt: 0, schemaExt: 0},
			clusterMembers:    []versions{{schemaInt: 1, schemaExt: 0}, {schemaInt: 0, schemaExt: 1}},
			expectErr:         fmt.Errorf("This node's version is behind, please upgrade"),
		},
		{
			name:              "All other nodes ahead (both internal/external)",
			upgradedLocalInfo: versions{schemaInt: 0, schemaExt: 0},
			clusterMembers:    []versions{{schemaInt: 1, schemaExt: 1}, {schemaInt: 1, schemaExt: 1}},
			expectErr:         fmt.Errorf("This node's version is behind, please upgrade"),
		},
		{
			name:              "All other nodes ahead (other node is waiting)",
			upgradedLocalInfo: versions{schemaInt: 0, schemaExt: 0},
			clusterMembers:    []versions{{schemaInt: 2, schemaExt: 2}, {schemaInt: 1, schemaExt: 1}},
			expectErr:         fmt.Errorf("This node's version is behind, please upgrade"),
		},
		{
			name:              "Some other nodes ahead (internal only)",
			upgradedLocalInfo: versions{schemaInt: 0, schemaExt: 0},
			clusterMembers:    []versions{{schemaInt: 0, schemaExt: 0}, {schemaInt: 1, schemaExt: 0}},
			expectErr:         fmt.Errorf("This node's version is behind, please upgrade"),
		},
		{
			name:              "Some other nodes ahead (external only)",
			upgradedLocalInfo: versions{schemaInt: 0, schemaExt: 0},
			clusterMembers:    []versions{{schemaInt: 0, schemaExt: 0}, {schemaInt: 0, schemaExt: 1}},
			expectErr:         fmt.Errorf("This node's version is behind, please upgrade"),
		},
		{
			name:              "Some other nodes ahead (both internal/external)",
			upgradedLocalInfo: versions{schemaInt: 0, schemaExt: 0},
			clusterMembers:    []versions{{schemaInt: 0, schemaExt: 0}, {schemaInt: 1, schemaExt: 1}},
			expectErr:         fmt.Errorf("This node's version is behind, please upgrade"),
		},
		{
			name:              "All other nodes behind (internal only)",
			upgradedLocalInfo: versions{schemaInt: 1, schemaExt: 0},
			clusterMembers:    []versions{{schemaInt: 0, schemaExt: 0}, {schemaInt: 0, schemaExt: 0}},
			expectWait:        true,
		},
		{
			name:              "All other nodes behind (external only)",
			upgradedLocalInfo: versions{schemaInt: 0, schemaExt: 1},
			clusterMembers:    []versions{{schemaInt: 0, schemaExt: 0}, {schemaInt: 0, schemaExt: 0}},
			expectWait:        true,
		},
		{
			name:              "All other nodes behind (both internal/external)",
			upgradedLocalInfo: versions{schemaInt: 1, schemaExt: 1},
			clusterMembers:    []versions{{schemaInt: 0, schemaExt: 0}, {schemaInt: 0, schemaExt: 0}},
			expectWait:        true,
		},
		{
			name:              "All other nodes behind (other node is waiting)",
			upgradedLocalInfo: versions{schemaInt: 3, schemaExt: 2},
			clusterMembers:    []versions{{schemaInt: 2, schemaExt: 2}, {schemaInt: 1, schemaExt: 1}},
			expectWait:        true,
		},
		{
			name:              "Some other nodes behind (internal only)",
			upgradedLocalInfo: versions{schemaInt: 1, schemaExt: 0},
			clusterMembers:    []versions{{schemaInt: 0, schemaExt: 0}, {schemaInt: 1, schemaExt: 0}},
			expectWait:        true,
		},
		{
			name:              "Some other nodes behind (external only)",
			upgradedLocalInfo: versions{schemaInt: 0, schemaExt: 1},
			clusterMembers:    []versions{{schemaInt: 0, schemaExt: 0}, {schemaInt: 0, schemaExt: 1}},
			expectWait:        true,
		},
		{
			name:              "Some other nodes behind (both internal/external)",
			upgradedLocalInfo: versions{schemaInt: 1, schemaExt: 1},
			clusterMembers:    []versions{{schemaInt: 0, schemaExt: 0}, {schemaInt: 1, schemaExt: 1}},
			expectWait:        true,
		},
		{
			name:              "Some nodes behind, others ahead (both internal/external)",
			upgradedLocalInfo: versions{schemaInt: 1, schemaExt: 1},
			clusterMembers:    []versions{{schemaInt: 0, schemaExt: 0}, {schemaInt: 2, schemaExt: 2}},
			expectErr:         fmt.Errorf("This node's version is behind, please upgrade"),
		},
	}

	for i, t := range tests {
		s.T().Logf("%s (case %d)", t.name, i)

		db, err := NewTestDB([]schema.Update{})
		s.NoError(err)

		ctx := context.Background()
		tx, err := db.db.BeginTx(ctx, nil)
		s.NoError(err)

		// Generate a cluster member for the local node.
		_, err = cluster.CreateInternalClusterMember(ctx, tx, cluster.InternalClusterMember{
			Name:           fmt.Sprintf("cluster-member-%d", 0),
			Address:        fmt.Sprintf("10.0.0.%d:8443", 0),
			Certificate:    fmt.Sprintf("test-cert-%d", 0),
			SchemaInternal: 0,
			SchemaExternal: 0,
			Heartbeat:      time.Time{},
			Role:           "voter",
		})

		s.NoError(err)

		// Generate a cluster member entry for all other expected nodes.
		for j, clusterMember := range t.clusterMembers {
			_, err = cluster.CreateInternalClusterMember(ctx, tx, cluster.InternalClusterMember{
				Name:           fmt.Sprintf("cluster-member-%d", j+1),
				Address:        fmt.Sprintf("10.0.0.%d:8443", j+1),
				Certificate:    fmt.Sprintf("test-cert-%d", j+1),
				SchemaInternal: clusterMember.schemaInt + 1,
				SchemaExternal: clusterMember.schemaExt + 1,
				Heartbeat:      time.Time{},
				Role:           "voter",
			})

			s.NoError(err)
		}

		s.NoError(tx.Commit())

		// Reset the local schema so we can override the updates.
		_, err = db.db.Exec("delete from schemas")
		s.NoError(err)

		stmt := `INSERT INTO schemas (version, type, updated_at) VALUES (?, ?, strftime("%s"))`

		// Apply the local updates to the schema table.
		manager := &update.SchemaUpdateManager{}
		updates := []schema.Update{}
		for j := 0; j < int(t.upgradedLocalInfo.schemaInt)+1; j++ {
			_, err = db.db.Exec(stmt, j, 0)
			s.NoError(err)

			updates = append(updates, func(ctx context.Context, tx *sql.Tx) error { return nil })
		}

		manager.SetInternalUpdates(updates)

		updates = []schema.Update{}
		for j := 0; j < int(t.upgradedLocalInfo.schemaExt)+1; j++ {
			_, err = db.db.Exec(stmt, j, 1)
			s.NoError(err)

			updates = append(updates, func(ctx context.Context, tx *sql.Tx) error { return nil })
		}

		manager.SetExternalUpdates(updates)

		if t.expectWait {
			db.upgradeCh <- struct{}{}
		}

		// Set a no-op schema manager so that we can go through the schema upgrade logic without running any real updates.
		db.schema = manager.Schema()

		// Run the upgrade function to wait, error, or succeed.
		err = db.waitUpgrade(false)
		if t.expectErr != nil {
			s.Equal(t.expectErr, err)
		} else if t.expectWait {
			s.Equal(schema.ErrGracefulAbort, err)
		} else {
			s.NoError(err)
		}

		tx, err = db.db.BeginTx(ctx, nil)
		s.NoError(err)

		schemaInternal, err := query.SelectIntegers(ctx, tx, "SELECT schema_internal FROM internal_cluster_members ORDER BY id")
		s.NoError(err)

		schemaExternal, err := query.SelectIntegers(ctx, tx, "SELECT schema_external FROM internal_cluster_members ORDER BY id")
		s.NoError(err)

		s.NoError(tx.Commit())

		s.Equal(len(t.clusterMembers)+1, len(schemaInternal))
		s.Equal(len(t.clusterMembers)+1, len(schemaExternal))

		// If there's an error, the internal_cluster_members table will be rolled back.
		minVersion := t.upgradedLocalInfo.schemaInt + 1
		if t.expectErr != nil {
			minVersion = 0
		}

		internalVersions := []int{int(minVersion)}
		for _, c := range t.clusterMembers {
			internalVersions = append(internalVersions, int(c.schemaInt)+1)
		}

		// If there's an error, the internal_cluster_members table will be rolled back.
		minVersion = t.upgradedLocalInfo.schemaExt + 1
		if t.expectErr != nil {
			minVersion = 0
		}

		externalVersions := []int{int(minVersion)}
		for _, c := range t.clusterMembers {
			externalVersions = append(externalVersions, int(c.schemaExt)+1)
		}

		s.Equal(internalVersions, schemaInternal)
		s.Equal(externalVersions, schemaExternal)
	}
}

// NewTedb returns a sqlite DB set up with the default microcluster schema.
func NewTestDB(extensionsExternal []schema.Update) (*DB, error) {
	var err error
	db := &DB{ctx: context.Background(), listenAddr: *api.NewURL().Host("10.0.0.0:8443"), upgradeCh: make(chan struct{}, 1)}
	db.db, err = sql.Open("sqlite3", ":memory:")
	if err != nil {
		return nil, err
	}

	db.SetSchema(extensionsExternal)
	_, err = db.schema.Ensure(db.db)
	if err != nil {
		return nil, err
	}

	err = cluster.PrepareStmts(db.db, cluster.GetCallerProject(), false)
	if err != nil {
		return nil, err
	}

	return db, nil
}
