package types

// DatabaseStatus is the current status of the database.
type DatabaseStatus string

const (
	// DatabaseReady indicates the database is open for use.
	DatabaseReady DatabaseStatus = "Database is online"

	// DatabaseWaiting indicates the database is blocked on a schema or API extension upgrade.
	DatabaseWaiting DatabaseStatus = "Database is waiting for an upgrade"

	// DatabaseStarting indicates the daemon is running, but dqlite is still in the process of starting up.
	DatabaseStarting DatabaseStatus = "Database is still starting"

	// DatabaseNotReady indicates the database is not yet ready for use.
	DatabaseNotReady DatabaseStatus = "Database is not yet initialized"

	// DatabaseOffline indicates that the database is offline.
	DatabaseOffline DatabaseStatus = "Database is offline"
)
