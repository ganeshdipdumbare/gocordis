package cordis

import (
	"fmt"
	"sync"
	"sync/atomic"
)

type isolateScope struct {
	parent  *isolateScope
	entries map[string]string
}

func (s *isolateScope) realm(name string) (string, bool) {
	for cur := s; cur != nil; cur = cur.parent {
		if realm, ok := cur.entries[name]; ok {
			return realm, true
		}
	}
	return "", false
}

type interceptNode struct {
	parent *interceptNode
	name   string
	value  any
}

// Context is a view of the plugin container. Extend, Isolate and Intercept
// return derived views; services and lifecycle state are shared with the root.
type Context struct {
	baseURL   string
	fiber     *Fiber
	root      *Context
	scope     *isolateScope
	intercept *interceptNode
	reflect   *reflectStore
	registry  *RegistryService
	events    *EventsService
	logger    *LoggerService
	engine    *engine
	mu        *sync.RWMutex
	state     *rootState
}

type rootState struct {
	closing    atomic.Bool
	isolateSeq atomic.Uint64
}

type reflectStore struct {
	store map[string]*impl
}

// New creates a root context with the built-in services.
func New() *Context {
	c := &Context{
		scope:  &isolateScope{entries: map[string]string{}},
		mu:     &sync.RWMutex{},
		state:  &rootState{},
		engine: newEngine(),
	}
	c.root = c
	c.reflect = &reflectStore{store: map[string]*impl{}}
	c.registry = &RegistryService{ctx: c, runtimes: map[*Plugin]*Runtime{}}
	c.events = &EventsService{ctx: c, hooks: map[string][]*hook{}}
	c.logger = &LoggerService{ctx: c, exporters: map[int]Exporter{}, bufferSize: 1000}
	c.fiber = &Fiber{
		uid:        0,
		state:      StateActive,
		ctx:        c,
		inject:     map[string]any{},
		store:      map[string]*impl{},
		provisions: map[*impl]struct{}{},
	}
	return c
}

// Extend returns a derived view that shares services, isolation and lifecycle.
func (c *Context) Extend() *Context {
	clone := *c
	return &clone
}

// Fiber returns the lifecycle object this context belongs to.
func (c *Context) Fiber() *Fiber { return c.fiber }

// BaseURL is the base location used by the loader.
func (c *Context) BaseURL() string { return c.baseURL }

// SetBaseURL changes the base location used by the loader.
func (c *Context) SetBaseURL(url string) { c.baseURL = url }

// Isolate makes name resolve against a separate realm in the derived context.
// Services provided there are invisible to contexts with a different label.
// An empty label generates a fresh realm, as in the paper.
func (c *Context) Isolate(name, label string) *Context {
	if label == "" {
		label = fmt.Sprintf("realm-%d", c.state.isolateSeq.Add(1))
	}
	derived := c.Extend()
	derived.scope = &isolateScope{
		parent:  c.scope,
		entries: map[string]string{name: realmID(name, label)},
	}
	return derived
}

// Closing reports whether the root context has begun disposal.
func (c *Context) Closing() bool { return c.state.closing.Load() }

// Intercept stores a config override inherited by derived contexts.
func (c *Context) Intercept(name string, config any) *Context {
	derived := c.Extend()
	derived.intercept = &interceptNode{parent: c.intercept, name: name, value: config}
	return derived
}

// InterceptValue returns the merged intercept metadata for name. Nearer
// intercepts take priority; maps are merged, other values replace.
func (c *Context) InterceptValue(name string) (any, bool) {
	var chain []any
	for cur := c.intercept; cur != nil; cur = cur.parent {
		if cur.name == name {
			chain = append(chain, cur.value)
		}
	}
	if len(chain) == 0 {
		return nil, false
	}
	var result any
	for i := len(chain) - 1; i >= 0; i-- {
		result = mergeMetadata(result, chain[i])
	}
	return result, true
}

func mergeMetadata(base, next any) any {
	if base == nil {
		return next
	}
	baseMap, baseOK := base.(map[string]any)
	nextMap, nextOK := next.(map[string]any)
	if !baseOK || !nextOK {
		return next
	}
	merged := make(map[string]any, len(baseMap)+len(nextMap))
	for key, value := range baseMap {
		merged[key] = value
	}
	for key, value := range nextMap {
		merged[key] = value
	}
	return merged
}

// EffectSeq registers an effect iterator. Each yielded disposer is kept and
// run in reverse on disposal; iteration stops when the fiber's target changes.
func (c *Context) EffectSeq(label string, fn func(yield func(Disposer) bool)) (Disposer, error) {
	return c.fiber.effectSeq(label, fn)
}

// Effect runs fn and keeps its disposer as a reversible registration of the
// current fiber. Registrations made inside fn are disposed before it.
func (c *Context) Effect(label string, fn func() (Disposer, error)) (Disposer, error) {
	return c.fiber.effect(label, fn)
}

// Dispose tears down every registration of the root context.
func (c *Context) Dispose() {
	c.state.closing.Store(true)
	c.engine.run(func() {
		for i := len(c.fiber.effects) - 1; i >= 0; i-- {
			c.fiber.effects[i].dispose()
		}
		c.fiber.effects = nil
	})
}

// Plugin mounts a plugin under this context.
func (c *Context) Plugin(p *Plugin, config any) *Fiber {
	return c.registry.PluginAt(c, p, config)
}

// Use is the paper's name for mounting a component.
func (c *Context) Use(p *Plugin, config any) *Fiber { return c.Plugin(p, config) }

func (c *Context) String() string {
	return fmt.Sprintf("Context<%s>", c.fiber.Name())
}
