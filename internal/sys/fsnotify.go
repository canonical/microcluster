package sys

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/canonical/lxd/shared"
	"github.com/fsnotify/fsnotify"

	"github.com/canonical/microcluster/v3/internal/log"
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

	logger, err := log.LoggerFromContext(ctx)
	if err != nil {
		return nil, err
	}

	// Listen for events across the given root dir.
	err = watcher.watchDir(root)
	if err != nil {
		closeErr := watcher.Close()
		if closeErr != nil {
			logger.Error("Failed to close filesystem watcher", slog.String("error", closeErr.Error()))
		}

		return nil, err
	}

	go watcher.handleEvents(ctx, logger)

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

func (w *Watcher) handleEvents(ctx context.Context, logger *slog.Logger) {
	for {
		select {
		case <-ctx.Done():
			logger.Info("Closing filesystem watcher")
			err := w.Close()
			if err != nil {
				logger.Error("Failed to close filesystem watcher", slog.String("error", err.Error()))
			}

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
					logger.Error("Failed executing action on fsnotify event", slog.String("event", event.Op.String()), slog.String("path", event.Name), slog.String("error", err.Error()))
				}
			}
			w.mu.Unlock()
		}
	}
}

// Watch adds a hook to be executed on create/remove events on files with the given extension under the given path.
func (w *Watcher) Watch(path string, fileExt string, f func(path string, event fsnotify.Op) error) error {
	if !strings.HasPrefix(path, w.root) {
		return fmt.Errorf("Path %q does not exist on watcher root path %q", path, w.root)
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
	return nil
}
