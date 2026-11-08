// Package fs provides file watching as a cordis service.
package fs

import (
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/bmatcuk/doublestar/v4"
	"github.com/fsnotify/fsnotify"

	"gocordis"
)

// Key is the file-system service.
var Key = cordis.NewKey[*Service]("fs")

// Service watches files and directories.
type Service struct {
	ctx *cordis.Context
}

// New registers the service on ctx.
func New(ctx *cordis.Context) (*Service, error) {
	service := &Service{ctx: ctx}
	if _, err := ctx.Provide(Key, service); err != nil {
		return nil, err
	}
	return service, nil
}

// WatchOptions filters and debounces change events.
type WatchOptions struct {
	// Filter holds glob patterns relative to the watched root. Empty means all.
	Filter []string
}

// Watcher delivers changed absolute paths until closed or its fiber unloads.
type Watcher struct {
	Events <-chan string

	done      chan struct{}
	closeOnce sync.Once
	closeFn   func() error
}

// Close stops the watch.
func (w *Watcher) Close() error {
	var err error
	w.closeOnce.Do(func() {
		close(w.done)
		if w.closeFn != nil {
			err = w.closeFn()
		}
	})
	return err
}

// Watch starts watching path. A directory is watched recursively.
func (s *Service) Watch(path string, options ...WatchOptions) (*Watcher, error) {
	root, err := filepath.Abs(path)
	if err != nil {
		return nil, err
	}
	var filter []string
	if len(options) > 0 {
		filter = options[0].Filter
	}

	events := make(chan string, 64)
	done := make(chan struct{})
	watcher := &Watcher{Events: events, done: done}

	dispose, err := s.ctx.Effect("fs.watch()", func() (cordis.Disposer, error) {
		backend, err := fsnotify.NewWatcher()
		if err != nil {
			return nil, err
		}
		if err := addAll(backend, root); err != nil {
			_ = backend.Close()
			return nil, err
		}
		go func() {
			defer close(events)
			for {
				select {
				case <-done:
					return
				case event, ok := <-backend.Events:
					if !ok {
						return
					}
					if event.Op&(fsnotify.Write|fsnotify.Create|fsnotify.Rename|fsnotify.Remove) == 0 {
						continue
					}
					if event.Op&fsnotify.Create != 0 {
						if info, err := os.Stat(event.Name); err == nil && info.IsDir() {
							_ = addAll(backend, event.Name)
						}
					}
					if !matches(root, event.Name, filter) {
						continue
					}
					select {
					case events <- event.Name:
					case <-done:
						return
					}
				case err, ok := <-backend.Errors:
					if !ok {
						return
					}
					s.ctx.Logger("fs").Warn(err)
				}
			}
		}()
		return backend.Close, nil
	})
	if err != nil {
		return nil, err
	}
	watcher.closeFn = func() error { return dispose() }
	return watcher, nil
}

func addAll(watcher *fsnotify.Watcher, root string) error {
	info, err := os.Stat(root)
	if err != nil {
		return err
	}
	if !info.IsDir() {
		return watcher.Add(root)
	}
	return filepath.WalkDir(root, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() {
			return watcher.Add(path)
		}
		return nil
	})
}

func matches(root, name string, filter []string) bool {
	if len(filter) == 0 {
		return true
	}
	relative, err := filepath.Rel(root, name)
	if err != nil {
		return false
	}
	relative = filepath.ToSlash(relative)
	for _, pattern := range filter {
		if ok, _ := doublestar.Match(filepath.ToSlash(pattern), relative); ok {
			return true
		}
	}
	return false
}

// Debounce folds events for key until delay passes without another event.
func Debounce(delay time.Duration, emit func(string)) func(string) {
	var mu sync.Mutex
	var timer *time.Timer
	pending := map[string]struct{}{}
	return func(path string) {
		mu.Lock()
		defer mu.Unlock()
		pending[path] = struct{}{}
		if timer != nil {
			timer.Stop()
		}
		timer = time.AfterFunc(delay, func() {
			mu.Lock()
			batch := pending
			pending = map[string]struct{}{}
			timer = nil
			mu.Unlock()
			for path := range batch {
				emit(path)
			}
		})
	}
}
