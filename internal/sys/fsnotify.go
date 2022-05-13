package sys

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/fsnotify/fsnotify"
	"github.com/lxc/lxd/shared"

	"github.com/canonical/microcluster/internal/logger"
)

// Watcher represents an fsnotify watcher.
type Watcher struct {
	*fsnotify.Watcher

	mu sync.Mutex

	watching map[string]func(string, fsnotify.Op) error
	root     string
}

// NewWatcher returns a watcher listening for fsnotify events down the given dir.
func NewWatcher(ctx context.Context, root string) (*Watcher, error) {
	fsWatcher, err := fsnotify.NewWatcher()
	if err != nil {
		return nil, err
	}

	watcher := &Watcher{
		Watcher:  fsWatcher,
		watching: map[string]func(string, fsnotify.Op) error{},
		root:     root,
	}

	// Listen for events across the given root dir.
	err = watcher.watchDir(root)
	if err != nil {
		watcher.Close()
		return nil, err
	}

	go watcher.handleEvents(ctx)

	return watcher, nil
}

// watchDir adds walks through the path and adds each file/dir to fsnotify's watchlist.
func (w *Watcher) watchDir(path string) error {
	if !shared.PathExists(path) {
		return fmt.Errorf("Path does not exist")
	}

	err := filepath.Walk(path, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return fmt.Errorf("Error visiting path %q: %w", path, err)
		}

		err = w.Add(path)
		if err != nil {
			return fmt.Errorf("Failed to watch path %q: %w", path, err)
		}

		return nil
	})

	return err
}

func (w *Watcher) handleEvents(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			logger.Info("Closing filesystem watcher")
			w.Close()
			return
		case event := <-w.Events:
			// Only handle write/remove events.
			if event.Op&fsnotify.Write == 0 && event.Op&fsnotify.Remove == 0 && event.Op&fsnotify.Create == 0 {
				continue
			}

			w.mu.Lock()
			for path, f := range w.watching {
				// Only handle watched events.
				if !strings.HasPrefix(event.Name, path) {
					continue
				}

				// Ignore matching directories.
				stat, err := os.Lstat(event.Name)
				if err == nil && stat.IsDir() {
					continue
				}

				// Event hook.
				err = f(event.Name, event.Op)
				if err != nil {
					logger.Errorf("Error executing action on fsnotify event %q for path %q: %v", event.Op.String(), event.Name, err)
				}
			}
			w.mu.Unlock()
		}
	}
}

// Watch adds a hook to be executed on create/remove events on files with the given extension under the given path.
func (w *Watcher) Watch(path string, fileExt string, f func(path string, event fsnotify.Op) error) {
	if !strings.HasPrefix(path, w.root) {
		logger.Errorf("Path %q does not exist on watcher root path %q", path, w.root)
		return
	}

	w.mu.Lock()
	defer w.mu.Unlock()

	fileExtHook := func(path string, event fsnotify.Op) error {
		if strings.HasSuffix(path, fileExt) {
			return f(path, event)
		}

		return nil
	}

	w.watching[path] = fileExtHook
}
