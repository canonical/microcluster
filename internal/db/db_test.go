package db

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"testing"
	"time"

	"github.com/canonical/lxd/lxd/db/query"
	"github.com/canonical/lxd/lxd/db/schema"
	"github.com/canonical/lxd/shared/api"
	"github.com/stretchr/testify/suite"

	"github.com/canonical/microcluster/cluster"
	"github.com/canonical/microcluster/internal/db/update"
	"github.com/canonical/microcluster/internal/extensions"
	"github.com/canonical/microcluster/internal/sys"
)

type dbSuite struct {
	suite.Suite
}

func TestDBSuite(t *testing.T) {
	suite.Run(t, new(dbSuite))
}

// Ensures waitUpgrade properly waits or returns an error with mismatched cluster versions.
func (s *dbSuite) Test_waitUpgradeSchema() {
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

		apiExtensions, err := extensions.NewExtensionRegistry(true)
		s.NoError(err)

		// Generate a cluster member for the local node.
		_, err = cluster.CreateCoreClusterMember(ctx, tx, cluster.CoreClusterMember{
			Name:           fmt.Sprintf("cluster-member-%d", 0),
			Address:        fmt.Sprintf("10.0.0.%d:8443", 0),
			Certificate:    fmt.Sprintf("test-cert-%d", 0),
			SchemaInternal: 0,
			SchemaExternal: 0,
			APIExtensions:  apiExtensions,
			Heartbeat:      time.Time{},
			Role:           "voter",
		})

		s.NoError(err)

		// Generate a cluster member entry for all other expected nodes.
		for j, clusterMember := range t.clusterMembers {
			_, err = cluster.CreateCoreClusterMember(ctx, tx, cluster.CoreClusterMember{
				Name:           fmt.Sprintf("cluster-member-%d", j+1),
				Address:        fmt.Sprintf("10.0.0.%d:8443", j+1),
				Certificate:    fmt.Sprintf("test-cert-%d", j+1),
				SchemaInternal: clusterMember.schemaInt + 1,
				SchemaExternal: clusterMember.schemaExt + 1,
				APIExtensions:  apiExtensions,
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
		err = db.waitUpgrade(false, apiExtensions)
		if t.expectErr != nil {
			s.Equal(t.expectErr, err)
		} else if t.expectWait {
			s.Equal(schema.ErrGracefulAbort, err)
		} else {
			s.NoError(err)
		}

		tx, err = db.db.BeginTx(ctx, nil)
		s.NoError(err)

		schemaInternal, err := query.SelectIntegers(ctx, tx, "SELECT schema_internal FROM core_cluster_members ORDER BY id")
		s.NoError(err)

		schemaExternal, err := query.SelectIntegers(ctx, tx, "SELECT schema_external FROM core_cluster_members ORDER BY id")
		s.NoError(err)

		s.NoError(tx.Commit())

		s.Equal(len(t.clusterMembers)+1, len(schemaInternal))
		s.Equal(len(t.clusterMembers)+1, len(schemaExternal))

		// If there's an error, the core_cluster_members table will be rolled back.
		minVersion := t.upgradedLocalInfo.schemaInt + 1
		if t.expectErr != nil {
			minVersion = 0
		}

		internalVersions := []int{int(minVersion)}
		for _, c := range t.clusterMembers {
			internalVersions = append(internalVersions, int(c.schemaInt)+1)
		}

		// If there's an error, the core_cluster_members table will be rolled back.
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

func (s *dbSuite) Test_waitUpgradeAPI() {
	tests := []struct {
		name                       string
		upgradedLocalAPIExtensions extensions.Extensions
		clusterMembersExtensions   []extensions.Extensions
		expectErr                  error
		expectWait                 bool
	}{
		{
			name:                       "No upgrade, no other nodes",
			upgradedLocalAPIExtensions: extensions.Extensions{},
		},
		{
			name:                       "API upgrade, no other nodes",
			upgradedLocalAPIExtensions: extensions.Extensions{"internal:a", "ext"},
		},
		{
			name:                       "All other nodes ahead",
			upgradedLocalAPIExtensions: extensions.Extensions{"internal:a", "ext"},
			clusterMembersExtensions:   []extensions.Extensions{{"internal:a", "ext", "ext2"}, {"internal:a", "ext", "ext2"}},
			expectErr:                  fmt.Errorf("This node's API extensions are behind, please upgrade"),
		},
		{
			name:                       "Some other nodes ahead",
			upgradedLocalAPIExtensions: extensions.Extensions{"internal:a", "ext"},
			clusterMembersExtensions:   []extensions.Extensions{{"internal:a", "ext"}, {"internal:a", "ext", "ext2"}},
			expectErr:                  fmt.Errorf("This node's API extensions are behind, please upgrade"),
		},
		{
			name:                       "All other nodes behind",
			upgradedLocalAPIExtensions: extensions.Extensions{"internal:a", "ext", "ext2"},
			clusterMembersExtensions:   []extensions.Extensions{{"internal:a", "ext"}, {"internal:a", "ext"}},
			expectWait:                 true,
		},
		{
			name:                       "Some other nodes behind",
			upgradedLocalAPIExtensions: extensions.Extensions{"internal:a", "ext", "ext2"},
			clusterMembersExtensions:   []extensions.Extensions{{"internal:a", "ext", "ext2"}, {"internal:a", "ext"}},
			expectWait:                 true,
		},
		{
			name:                       "Some nodes behind, others ahead",
			upgradedLocalAPIExtensions: extensions.Extensions{"internal:a", "ext", "ext2"},
			clusterMembersExtensions:   []extensions.Extensions{{"internal:a", "ext"}, {"internal:a", "ext", "ext2", "ext3"}},
			expectErr:                  fmt.Errorf("This node's API extensions are behind, please upgrade"),
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
		_, err = cluster.CreateCoreClusterMember(ctx, tx, cluster.CoreClusterMember{
			Name:           fmt.Sprintf("cluster-member-%d", 0),
			Address:        fmt.Sprintf("10.0.0.%d:8443", 0),
			Certificate:    fmt.Sprintf("test-cert-%d", 0),
			SchemaInternal: 0,
			SchemaExternal: 0,
			APIExtensions:  t.upgradedLocalAPIExtensions,
			Heartbeat:      time.Time{},
			Role:           "voter",
		})

		s.NoError(err)

		// Generate a cluster member entry for all other expected nodes.
		for j, clusterMemberExtensions := range t.clusterMembersExtensions {
			_, err = cluster.CreateCoreClusterMember(ctx, tx, cluster.CoreClusterMember{
				Name:           fmt.Sprintf("cluster-member-%d", j+1),
				Address:        fmt.Sprintf("10.0.0.%d:8443", j+1),
				Certificate:    fmt.Sprintf("test-cert-%d", j+1),
				SchemaInternal: 1,
				SchemaExternal: 1,
				APIExtensions:  clusterMemberExtensions,
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

		_, err = db.db.Exec(stmt, 0, 0)
		s.NoError(err)
		updates := []schema.Update{func(ctx context.Context, tx *sql.Tx) error { return nil }}
		manager.SetInternalUpdates(updates)

		_, err = db.db.Exec(stmt, 0, 1)
		s.NoError(err)
		updates = []schema.Update{func(ctx context.Context, tx *sql.Tx) error { return nil }}
		manager.SetExternalUpdates(updates)

		if t.expectWait {
			db.upgradeCh <- struct{}{}
		}

		// Set a no-op schema manager so that we can go through the schema upgrade logic without running any real updates.
		db.schema = manager.Schema()

		// Run the upgrade function to wait, error, or succeed.
		err = db.waitUpgrade(false, t.upgradedLocalAPIExtensions)
		if t.expectErr != nil {
			s.Equal(t.expectErr, err)
		} else if t.expectWait {
			s.Equal(schema.ErrGracefulAbort, err)
		} else {
			s.NoError(err)
		}

		tx, err = db.db.BeginTx(ctx, nil)
		s.NoError(err)

		res, err := query.SelectStrings(ctx, tx, "SELECT api_extensions FROM core_cluster_members ORDER BY id")
		s.NoError(err)
		allExtensions := make([]extensions.Extensions, 0)
		for _, r := range res {
			e := extensions.Extensions{}
			err = json.Unmarshal([]byte(r), &e)
			s.NoError(err)
			allExtensions = append(allExtensions, e)
		}

		s.NoError(tx.Commit())

		s.Equal(len(t.clusterMembersExtensions)+1, len(allExtensions))

		if t.expectErr == nil && !t.expectWait {
			for _, c := range t.clusterMembersExtensions {
				err = t.upgradedLocalAPIExtensions.IsSameVersion(c)
				s.NoError(err)
			}
		}
	}
}

func (s *dbSuite) Test_waitUpgradeSchemaAndAPI() {
	type versionsWithExtensions struct {
		schemaInt uint64
		schemaExt uint64
		ext       extensions.Extensions
	}

	tests := []struct {
		name              string
		upgradedLocalInfo versionsWithExtensions
		clusterMembers    []versionsWithExtensions
		expectErr         error
		expectWait        bool
	}{
		{
			name: "Local node in sync, no other nodes",
			upgradedLocalInfo: versionsWithExtensions{
				schemaInt: 0,
				schemaExt: 0,
				ext:       extensions.Extensions{"internal:a"},
			},
		},
		{
			name: "Local node behind in schema and API",
			upgradedLocalInfo: versionsWithExtensions{
				schemaInt: 0,
				schemaExt: 0,
				ext:       extensions.Extensions{"internal:a"},
			},
			clusterMembers: []versionsWithExtensions{
				{schemaInt: 1, schemaExt: 1, ext: extensions.Extensions{"internal:a"}},
				{schemaInt: 1, schemaExt: 1, ext: extensions.Extensions{"internal:a"}},
			},
			expectErr: fmt.Errorf("This node's version is behind, please upgrade"),
		},
		{
			name: "Local node ahead, all other nodes behind",
			upgradedLocalInfo: versionsWithExtensions{
				schemaInt: 2,
				schemaExt: 2,
				ext:       extensions.Extensions{"internal:a", "b", "c"},
			},
			clusterMembers: []versionsWithExtensions{
				{schemaInt: 1, schemaExt: 1, ext: extensions.Extensions{"internal:a", "b", "c"}},
				{schemaInt: 1, schemaExt: 1, ext: extensions.Extensions{"internal:a", "b", "c"}},
			},
			expectWait: true,
		},
		{
			name: "Mixed node versions, some ahead and some behind",
			upgradedLocalInfo: versionsWithExtensions{
				schemaInt: 1,
				schemaExt: 1,
				ext:       extensions.Extensions{"internal:a", "b"},
			},
			clusterMembers: []versionsWithExtensions{
				{schemaInt: 2, schemaExt: 2, ext: extensions.Extensions{"internal:a", "b"}},
				{schemaInt: 0, schemaExt: 0, ext: extensions.Extensions{"internal:a", "b"}},
			},
			expectErr: fmt.Errorf("This node's version is behind, please upgrade"),
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
		_, err = cluster.CreateCoreClusterMember(ctx, tx, cluster.CoreClusterMember{
			Name:           fmt.Sprintf("cluster-member-%d", 0),
			Address:        fmt.Sprintf("10.0.0.%d:8443", 0),
			Certificate:    fmt.Sprintf("test-cert-%d", 0),
			SchemaInternal: 0,
			SchemaExternal: 0,
			APIExtensions:  extensions.Extensions{},
			Heartbeat:      time.Time{},
			Role:           "voter",
		})

		s.NoError(err)

		// Generate a cluster member entry for all other expected nodes.
		for j, clusterMember := range t.clusterMembers {
			_, err = cluster.CreateCoreClusterMember(ctx, tx, cluster.CoreClusterMember{
				Name:           fmt.Sprintf("cluster-member-%d", j+1),
				Address:        fmt.Sprintf("10.0.0.%d:8443", j+1),
				Certificate:    fmt.Sprintf("test-cert-%d", j+1),
				SchemaInternal: clusterMember.schemaInt + 1,
				SchemaExternal: clusterMember.schemaExt + 1,
				APIExtensions:  clusterMember.ext,
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
		err = db.waitUpgrade(false, t.upgradedLocalInfo.ext)
		if t.expectErr != nil {
			s.Equal(t.expectErr, err)
		} else if t.expectWait {
			s.Equal(schema.ErrGracefulAbort, err)
		} else {
			s.NoError(err)
		}

		tx, err = db.db.BeginTx(ctx, nil)
		s.NoError(err)

		schemaInternal, err := query.SelectIntegers(ctx, tx, "SELECT schema_internal FROM core_cluster_members ORDER BY id")
		s.NoError(err)

		schemaExternal, err := query.SelectIntegers(ctx, tx, "SELECT schema_external FROM core_cluster_members ORDER BY id")
		s.NoError(err)

		res, err := query.SelectStrings(ctx, tx, "SELECT api_extensions FROM core_cluster_members ORDER BY id")
		s.NoError(err)
		allExtensions := make([]extensions.Extensions, 0)
		for _, r := range res {
			e := extensions.Extensions{}
			err = json.Unmarshal([]byte(r), &e)
			s.NoError(err)
			allExtensions = append(allExtensions, e)
		}

		s.NoError(tx.Commit())

		s.Equal(len(t.clusterMembers)+1, len(schemaInternal))
		s.Equal(len(t.clusterMembers)+1, len(schemaExternal))

		// If there's an error, the core_cluster_members table will be rolled back.
		minVersion := t.upgradedLocalInfo.schemaInt + 1
		if t.expectErr != nil {
			minVersion = 0
		}

		internalVersions := []int{int(minVersion)}
		for _, c := range t.clusterMembers {
			internalVersions = append(internalVersions, int(c.schemaInt)+1)
		}

		// If there's an error, the core_cluster_members table will be rolled back.
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

		s.Equal(len(t.clusterMembers)+1, len(allExtensions))

		if t.expectErr == nil && !t.expectWait {
			for _, c := range t.clusterMembers {
				err = t.upgradedLocalInfo.ext.IsSameVersion(c.ext)
				s.NoError(err)
			}
		}
	}
}

// NewTedb returns a sqlite DB set up with the default microcluster schema.
func NewTestDB(extensionsExternal []schema.Update) (*DB, error) {
	var err error
	db := &DB{
		ctx:        context.Background(),
		listenAddr: *api.NewURL().Host("10.0.0.0:8443"),
		upgradeCh:  make(chan struct{}, 1),
		os:         &sys.OS{},
	}
	db.db, err = sql.Open("sqlite3", ":memory:")
	if err != nil {
		return nil, err
	}

	db.SetSchema(extensionsExternal, nil)
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
