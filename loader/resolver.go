package loader

import (
	"fmt"
	"sync"

	"gocordis"
)

// Resolver turns an entry name into a plugin.
type Resolver interface {
	Resolve(name string) (*cordis.Plugin, error)
}

// Catalog is a name-to-plugin registry. Plugins register themselves under a
// stable name; no code generation or dynamic loading is involved.
type Catalog struct {
	mu      sync.RWMutex
	plugins map[string]*cordis.Plugin
}

// NewCatalog creates an empty catalog.
func NewCatalog() *Catalog {
	return &Catalog{plugins: map[string]*cordis.Plugin{}}
}

// Add registers a plugin under name.
func (c *Catalog) Add(name string, plugin *cordis.Plugin) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.plugins[name] = plugin
}

// Resolve returns the plugin registered under name.
func (c *Catalog) Resolve(name string) (*cordis.Plugin, error) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	plugin := c.plugins[name]
	if plugin == nil {
		return nil, fmt.Errorf("plugin %q is not registered", name)
	}
	return plugin, nil
}
