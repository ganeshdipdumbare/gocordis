// Package pkgmanager installs Go dependencies for a project.
//
// Cordis' package-manager drives npm/yarn. The Go equivalent edits go.mod and
// go.sum through the go command, so that is what this adapter runs.
package pkgmanager

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"gocordis"
)

// Key is the package-manager service.
var Key = cordis.NewKey[*Service]("packageManager")

// Config locates the module and bounds the go command.
type Config struct {
	Root    string
	Timeout time.Duration
}

// Service runs go commands in the module root.
type Service struct {
	root    string
	timeout time.Duration
	ctx     *cordis.Context
	mu      sync.Mutex
}

// New finds the nearest go.mod and registers the service.
func New(ctx *cordis.Context, config Config) (*Service, error) {
	root := config.Root
	if root == "" {
		root = ctx.BaseURL()
	}
	if root == "" {
		root = "."
	}
	absolute, err := filepath.Abs(root)
	if err != nil {
		return nil, err
	}
	found, err := findModule(absolute)
	if err != nil {
		return nil, err
	}
	if config.Timeout <= 0 {
		config.Timeout = 10 * time.Minute
	}
	service := &Service{root: found, timeout: config.Timeout, ctx: ctx}
	if _, err := ctx.Provide(Key, service); err != nil {
		return nil, err
	}
	return service, nil
}

// Root is the directory containing go.mod.
func (s *Service) Root() string { return s.root }

// Install runs go get for the given packages.
func (s *Service) Install(ctx context.Context, packages ...string) error {
	if len(packages) == 0 {
		return nil
	}
	return s.run(ctx, append([]string{"get"}, packages...)...)
}

// Remove runs go mod tidy after dropping the packages from the build.
func (s *Service) Remove(ctx context.Context, _ ...string) error {
	return s.run(ctx, "mod", "tidy")
}

func (s *Service) run(ctx context.Context, args ...string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, s.timeout)
		defer cancel()
	}
	command := exec.CommandContext(ctx, "go", args...)
	command.Dir = s.root
	output, err := command.CombinedOutput()
	s.ctx.Logger("packageManager").Debug(strings.TrimSpace(string(output)))
	if err != nil {
		return fmt.Errorf("go %s: %w: %s", strings.Join(args, " "), err, strings.TrimSpace(string(output)))
	}
	return nil
}

func findModule(start string) (string, error) {
	dir := start
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir, nil
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", fmt.Errorf("no go.mod found from %s", start)
		}
		dir = parent
	}
}
