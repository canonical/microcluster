package trust

import (
	"fmt"
	"sync"

	"github.com/fsnotify/fsnotify"

	"github.com/canonical/microcluster/internal/sys"
)

// Store represents a directory of remotes watched by the fsnotify Watcher.
type Store struct {
	remotesMu sync.RWMutex // Mutex for coordinating manual and fsnotify access to remotes.

	remotes map[Role]*Remotes // Should never be called directly, instead use Remotes().

	refresh func() error
}

// Init initializes the remotes in the truststore, seeds the rand package for selecting remotes at random, and watches
// the truststore directory for updates.
func Init(watcher *sys.Watcher, onUpdate func(oldRemotes, newRemotes Remotes) error, dir string) (*Store, error) {
	ts := &Store{remotes: map[Role]*Remotes{Cluster: {role: Cluster}, NonCluster: {role: NonCluster}}}
	ts.remotesMu.Lock()
	defer ts.remotesMu.Unlock()

	for role := range ts.remotes {
		ts.remotes[role].data = map[string]Remote{}
		err := ts.remotes[role].Load(dir)
		if err != nil {
			return nil, err
		}
	}

	ts.refresh = func() error {
		ts.remotesMu.Lock()
		defer func() {
			ts.remotesMu.Unlock()
		}()

		for role := range ts.remotes {
			err := ts.remotes[role].Load(dir)
			if err != nil {
				return fmt.Errorf("Unable to refresh remotes: %w", err)
			}
		}

		return nil
	}

	// Watch on the truststore directory for yaml updates.
	watcher.Watch(dir, "yaml", func(path string, event fsnotify.Op) error {
		return ts.refresh()
	})

	return ts, nil
}

// Remotes returns a thread-safe list of the remotes in the truststore, as watched by fsnotify.
// If no role is specified, defaults to "cluster" for dqlite peers.
func (ts *Store) Remotes(role Role) *Remotes {
	ts.remotesMu.RLock()
	defer ts.remotesMu.RUnlock()

	if role == "" {
		role = Cluster
	}

	return ts.remotes[role]
}

// Refresh reloads the truststore and runs any associated hooks.
func (ts *Store) Refresh() error {
	return ts.refresh()
}
