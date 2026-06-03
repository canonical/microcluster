package sys

const (
	// DqliteSocket is the configurable location of the dqlite socket.
	DqliteSocket = "DQLITE_SOCKET"

	// DqliteReadyTimeout overrides how long to wait for dqlite to become ready
	// when opening the database (a Go duration string, e.g. "30m"). Defaults to
	// 2 minutes. Useful when a joining node must sync a large dqlite database
	// over a slow link or disk and would otherwise exceed the default timeout.
	DqliteReadyTimeout = "DQLITE_READY_TIMEOUT"

	// StateDir is the location of the daemon state directory.
	StateDir = "STATE_DIR"

	// SchemaUpdate is the path to the schema update to run.
	SchemaUpdate = "SCHEMA_UPDATE"

	// SocketGroup is the configurable group of the socket.
	SocketGroup = "SOCKET_GROUP"
)
