package cluster

import (
	"time"
)

// Schema represents the database schema table.
type Schema struct {
	ID        int
	Version   int `db:"primary=yes"`
	UpdatedAt time.Time
}
