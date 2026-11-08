// Package hmr reloads declarative config when watched files change.
//
// Go cannot reload compiled code in-process. A change to a plugin's source
// needs a restart; this service only re-runs registered reload callbacks, such
// as re-reading the include file. That is the config-level half of Cordis HMR.
package hmr

import (
	"errors"
	"sync"
	"time"

	"gocordis"
	"gocordis/fs"
)

// Key is the HMR service.
var Key = cordis.NewKey[*Service]("hmr")

// Config controls watching.
type Config struct {
	Base     string
	Watch    bool
	Debounce time.Duration
}

// Service re-runs reload callbacks on file changes.
type Service struct {
	ctx    *cordis.Context
	config Config

	mu       sync.Mutex
	handlers []func() error
}

// New creates the service and optionally starts watching the base directory.
func New(ctx *cordis.Context, config Config) (*Service, error) {
	service := &Service{ctx: ctx, config: config}
	if _, err := ctx.Provide(Key, service); err != nil {
		return nil, err
	}
	if config.Watch {
		if err := service.Watch(); err != nil {
			return nil, err
		}
	}
	return service, nil
}

// Handle registers a callback run after a file change.
func (s *Service) Handle(fn func() error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.handlers = append(s.handlers, fn)
}

// Reload runs every registered callback.
func (s *Service) Reload() error {
	s.mu.Lock()
	handlers := append([]func() error(nil), s.handlers...)
	s.mu.Unlock()
	for _, handler := range handlers {
		if err := handler(); err != nil {
			s.ctx.Logger("hmr").Warn(err)
		}
	}
	return nil
}

// Watch reloads on changes under the base directory.
func (s *Service) Watch(paths ...string) error {
	fsService, ok := s.ctx.Get(fs.Key)
	if !ok {
		return errors.New("hmr requires the fs service")
	}
	base := s.config.Base
	if base == "" {
		base = s.ctx.BaseURL()
	}
	if base == "" {
		base = "."
	}
	watcher, err := fsService.Watch(base, fs.WatchOptions{Filter: paths})
	if err != nil {
		return err
	}
	delay := s.config.Debounce
	if delay <= 0 {
		delay = 100 * time.Millisecond
	}
	debounced := fs.Debounce(delay, func(path string) {
		s.ctx.Logger("hmr").Info("%s changed, reloading config", path)
		if err := s.Reload(); err != nil {
			s.ctx.Logger("hmr").Warn(err)
		}
	})
	if _, err := s.ctx.Effect("hmr.watch()", func() (cordis.Disposer, error) {
		go func() {
			for path := range watcher.Events {
				debounced(path)
			}
		}()
		return watcher.Close, nil
	}); err != nil {
		_ = watcher.Close()
		return err
	}
	return nil
}
