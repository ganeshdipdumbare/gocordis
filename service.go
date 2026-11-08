package cordis

import (
	"fmt"
)

// Key identifies a service in a context. The type parameter is the value a
// lookup returns, so a key cannot be resolved against the wrong type.
type Key[T any] struct{ name string }

// NewKey creates a service key with a stable name.
func NewKey[T any](name string) *Key[T] { return &Key[T]{name: name} }

// Name is the service name, used by isolation and interception.
func (k *Key[T]) Name() string { return k.name }

// KeyName is satisfied by every *Key[T].
type KeyName interface{ Name() string }

// impl is one registration of a service in a realm.
type impl struct {
	uid        int64
	name       string
	realm      string
	ctx        *Context
	value      any
	check      func() bool
	retiring   bool
	dependents map[*Fiber]struct{}
}

// Get resolves a service from the context's realm.
func (c *Context) Get[T any](k *Key[T]) (T, bool) {
	var zero T
	im := c.lookup(k.Name())
	if im == nil {
		return zero, false
	}
	value, ok := im.value.(T)
	if !ok {
		return zero, false
	}
	return value, true
}

// MustGet resolves a service or panics.
func (c *Context) MustGet[T any](k *Key[T]) T {
	value, ok := c.Get(k)
	if !ok {
		panic(fmt.Sprintf("cordis: service %q is not available", k.Name()))
	}
	return value
}

// Require resolves a service through the committing fiber's view, matching
// the paper's point-of-use access: the fiber must have declared the key, and
// the declaration must have been committed by a successful load.
func (c *Context) Require[T any](k *Key[T]) (T, error) {
	var zero T
	name := k.Name()
	for fiber := c.fiber; fiber != nil; fiber = fiber.parent {
		if im, ok := fiber.committed[name]; ok {
			value, ok := im.value.(T)
			if !ok {
				return zero, fmt.Errorf("service %q has type %T, not %T", name, im.value, zero)
			}
			return value, nil
		}
		if _, ok := fiber.inject[name]; ok {
			return zero, fmt.Errorf("%w: %q", ErrInactiveAccess, name)
		}
		if fiber.parent == nil || fiber.parent == fiber {
			break
		}
	}
	return zero, fmt.Errorf("%w: %q", ErrUndeclaredAccess, name)
}

// Set replaces the value of a service provided by this context's fiber.
func (c *Context) Set[T any](k *Key[T], value T) error {
	im := c.lookup(k.Name())
	if im == nil {
		return fmt.Errorf("cannot set service %q without provide", k.Name())
	}
	if im.ctx.fiber != c.fiber {
		return fmt.Errorf("cannot set service %q owned by another fiber", k.Name())
	}
	im.value = value
	return nil
}

// Provide registers a service. The registration is an effect of the calling
// fiber: disposing it, or unloading the fiber, removes the service.
func (c *Context) Provide[T any](k *Key[T], value T) (Disposer, error) {
	return c.provide(k.Name(), value, nil)
}

func (c *Context) provide(name string, value any, check func() bool) (Disposer, error) {
	if !c.fiber.usable() {
		return nil, ErrInactive
	}
	return c.fiber.effect("ctx.provide("+name+")", func() (Disposer, error) {
		realm := c.allocateRealm(name)
		if old := c.reflect.store[realm]; old != nil {
			return nil, fmt.Errorf("service %q has been registered at <%s>", name, old.ctx.fiber.Name())
		}
		im := &impl{
			uid:        c.registry.nextImplUID(),
			name:       name,
			realm:      realm,
			ctx:        c,
			value:      value,
			check:      check,
			dependents: map[*Fiber]struct{}{},
		}
		c.reflect.store[realm] = im
		c.fiber.provisions[im] = struct{}{}
		c.engine.run(func() { c.registry.notify(name, realm) })
		return func() error {
			c.removeImpl(im)
			return nil
		}, nil
	})
}

func (c *Context) lookup(name string) *impl {
	realm, ok := c.realmOf(name)
	if !ok {
		return nil
	}
	return c.reflect.store[realm]
}

func (c *Context) realmOf(name string) (string, bool) {
	c.root.mu.RLock()
	defer c.root.mu.RUnlock()
	return c.scope.realm(name)
}

func (c *Context) allocateRealm(name string) string {
	c.root.mu.Lock()
	defer c.root.mu.Unlock()
	if realm, ok := c.scope.realm(name); ok {
		return realm
	}
	realm := realmID(name, "")
	c.root.scope.entries[name] = realm
	return realm
}

func (c *Context) removeImpl(im *impl) {
	// Retiring bindings are invisible to injection but still readable, so that
	// dependents can release while their teardown still sees the value.
	im.retiring = true
	for dep := range im.dependents {
		if dep.state == StateActive || dep.state == StateLoading {
			dep.refresh()
		}
	}
	if c.reflect.store[im.realm] == im {
		delete(c.reflect.store, im.realm)
	}
	delete(c.fiber.provisions, im)
	c.engine.run(func() { c.registry.notify(im.name, im.realm) })
}

func realmID(name, label string) string {
	if label == "" {
		return name
	}
	return name + "@" + label
}
