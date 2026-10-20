package cluster

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"
	"time"

	_ "github.com/mattn/go-sqlite3"
	"github.com/stretchr/testify/require"

	"github.com/canonical/microcluster/v4/internal/db/update"
	clusterDB "github.com/canonical/microcluster/v4/microcluster/db"
)

// commitFrames runs f in a transaction and returns the number of WAL frames the commit wrote. Each frame is one
// dirtied page, and dqlite replicates exactly these frames, so this is the size of the transaction's raft entry.
func commitFrames(t *testing.T, db *sql.DB, f func(tx *sql.Tx) error) int {
	var busy, frames, checkpointed int
	err := db.QueryRow("PRAGMA wal_checkpoint(TRUNCATE)").Scan(&busy, &frames, &checkpointed)
	require.NoError(t, err)

	tx, err := db.BeginTx(context.Background(), nil)
	require.NoError(t, err)

	err = f(tx)
	require.NoError(t, err)

	err = tx.Commit()
	require.NoError(t, err)

	err = db.QueryRow("PRAGMA wal_checkpoint(PASSIVE)").Scan(&busy, &frames, &checkpointed)
	require.NoError(t, err)

	return frames
}

// Ensures UpdateCoreClusterMemberHeartbeat writes the heartbeat and role of the given member, and dirties fewer pages
// than UpdateCoreClusterMember does, as it leaves the indexed columns alone.
func TestUpdateCoreClusterMemberHeartbeat(t *testing.T) {
	ctx := context.Background()

	// WAL mode needs a file, and makes the pages written by each transaction countable.
	db, err := sql.Open("sqlite3", filepath.Join(t.TempDir(), "db.sqlite"))
	require.NoError(t, err)
	defer db.Close()

	var journalMode string
	err = db.QueryRow("PRAGMA journal_mode=WAL").Scan(&journalMode)
	require.NoError(t, err)
	require.Equal(t, "wal", journalMode)

	_, err = update.NewSchema().Schema().Ensure(ctx, db)
	require.NoError(t, err)

	err = clusterDB.PrepareStmts(db, false)
	require.NoError(t, err)

	member := CoreClusterMember{
		Name:           "member-0",
		Address:        "10.0.0.0:8443",
		Certificate:    "test-cert-0",
		SchemaInternal: 1,
		SchemaExternal: 1,
		Heartbeat:      time.Now().Add(-time.Minute),
		Role:           "voter",
	}

	_ = commitFrames(t, db, func(tx *sql.Tx) error {
		_, err := CreateCoreClusterMember(ctx, tx, member)
		return err
	})

	member.Heartbeat = time.Now()
	fullFrames := commitFrames(t, db, func(tx *sql.Tx) error {
		return UpdateCoreClusterMember(ctx, tx, member.Name, member)
	})

	var before []CoreClusterMember
	_ = commitFrames(t, db, func(tx *sql.Tx) error {
		var err error
		before, err = GetCoreClusterMembers(ctx, tx)
		return err
	})

	now := time.Now()
	heartbeatFrames := commitFrames(t, db, func(tx *sql.Tx) error {
		return UpdateCoreClusterMemberHeartbeat(ctx, tx, before[0].ID, now, "spare")
	})

	// With a single row, the table and each index fit in one page, so the heartbeat update may dirty only the table's page.
	require.Equal(t, 1, heartbeatFrames, "Heartbeat update should dirty only the table page")
	require.Less(t, heartbeatFrames, fullFrames, "Heartbeat update should dirty fewer pages than a full update")

	var after []CoreClusterMember
	_ = commitFrames(t, db, func(tx *sql.Tx) error {
		var err error
		after, err = GetCoreClusterMembers(ctx, tx)
		return err
	})

	require.True(t, after[0].Heartbeat.Equal(now), "Heartbeat was not updated")
	before[0].Heartbeat = after[0].Heartbeat
	before[0].Role = "spare"
	require.Equal(t, before, after)
}
