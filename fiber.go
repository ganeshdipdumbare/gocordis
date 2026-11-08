package cordis

import (
	"errors"
	"fmt"
	"sort"
)

// Fiber is one lifecycle instance of a plugin. It owns the effects created
// while the plugin is mounted and reloads when an injected service changes.
type Fiber struct {
	uid     int64
	ctx     *Context
	parent  *Fiber
	runtime *Runtime
	state   State
	config  any
	inject  map[string]any

	store     map[string]*impl
	committed map[string]*impl

	provisions map[*impl]struct{}
	effects    []*effectNode

	currentEffect *effectNode

	revision int
	epoch    string
	err      error
}

// Name is the enclosing plugin name, or "root" for the root fiber.
func (f *Fiber) Name() string {
	for cur := f; cur != nil; cur = cur.parent {
		if cur.runtime != nil && cur.runtime.plugin.Name != "" {
			return cur.runtime.plugin.Name
		}
		if cur.parent == cur {
			break
		}
	}
	return "root"
}

// State reports the current lifecycle state.
func (f *Fiber) State() State { return f.state }

// UID is the fiber's stable identity; negative after disposal.
func (f *Fiber) UID() int64 { return f.uid }

// Context returns the context the plugin received.
func (f *Fiber) Context() *Context { return f.ctx }

// Config returns the last raw config passed to the plugin.
func (f *Fiber) Config() any { return f.config }

// Err returns the error that moved the fiber to the failed state.
func (f *Fiber) Err() error { return f.err }

func (f *Fiber) usable() bool {
	return f != nil && f.state != StateDisposed && f.uid >= 0
}

func (f *Fiber) assertUsable() {
	if !f.usable() {
		panic(ErrInactive)
	}
}

func (f *Fiber) setState(state State) {
	if f.state == state {
		return
	}
	old := f.state
	f.state = state
	if f.ctx != nil && f.ctx.events != nil {
		f.ctx.events.Emit("internal/status", f, old)
	}
}

// effect registers fn as a reversible registration. Nested registrations are
// disposed before their parent, all in reverse order.
func (f *Fiber) effect(label string, fn func() (Disposer, error)) (Disposer, error) {
	if !f.usable() {
		return nil, ErrInactive
	}
	node := &effectNode{label: label}
	parent := f.currentEffect
	if parent != nil {
		node.parent = parent
		parent.children = append(parent.children, node)
	} else {
		f.effects = append(f.effects, node)
	}
	f.currentEffect = node

	var disposer Disposer
	var err error
	func() {
		defer func() { f.currentEffect = parent }()
		disposer, err = fn()
	}()
	if err != nil {
		_ = node.dispose()
		return nil, err
	}
	node.disposer = disposer
	return node.dispose, nil
}

// effectSeq registers an iterator of inverses. The guard is checked before
// each yielded inverse, so a transition that changes the target stops
// collection at the next iteration boundary.
func (f *Fiber) effectSeq(label string, fn func(yield func(Disposer) bool)) (Disposer, error) {
	if !f.usable() {
		return nil, ErrInactive
	}
	node := &effectNode{label: label}
	parent := f.currentEffect
	if parent != nil {
		node.parent = parent
		parent.children = append(parent.children, node)
	} else {
		f.effects = append(f.effects, node)
	}
	f.currentEffect = node
	target := f.epoch
	armed := true
	var inverses []Disposer

	func() {
		defer func() { f.currentEffect = parent }()
		fn(func(disposer Disposer) bool {
			if !armed {
				return false
			}
			if f.epoch != target {
				armed = false
				return false
			}
			if disposer != nil {
				inverses = append(inverses, disposer)
			}
			return true
		})
	}()

	node.disposer = func() error {
		var errs []error
		for i := len(inverses) - 1; i >= 0; i-- {
			if err := inverses[i](); err != nil {
				errs = append(errs, err)
			}
		}
		return errors.Join(errs...)
	}
	return node.dispose, nil
}

// refresh recomputes the injection epoch and starts, reloads or unloads the
// plugin as needed. It must run inside the engine.
func (f *Fiber) refresh() {
	if f.uid < 0 || f.state == StateDisposed {
		return
	}
	// A failed fiber only recovers through Update, which clears the error.
	if f.state == StateFailed && f.err != nil {
		return
	}

	names := sortedKeys(f.inject)
	epoch := fmt.Sprint(f.revision)
	active := true
	next := make(map[string]*impl, len(names))
	for _, name := range names {
		im := f.checkImpl(name)
		if im == nil {
			active = false
			break
		}
		next[name] = im
		epoch += fmt.Sprintf(":%d.%d", im.uid, im.ctx.fiber.uid)
	}
	f.store = next
	if !active {
		epoch = "-"
	}
	if epoch == f.epoch {
		return
	}
	f.epoch = epoch

	if !active {
		if f.state == StateActive || f.state == StateLoading {
			f.unload(false)
		}
		return
	}
	switch f.state {
	case StateActive, StateLoading:
		f.unload(false)
		f.load()
	case StatePending, StateFailed:
		f.load()
	}
}

func (f *Fiber) checkImpl(name string) *impl {
	im := f.ctx.lookup(name)
	if im == nil || im.retiring {
		return nil
	}
	// Only a fully active provider may satisfy an injection.
	if im.ctx.fiber.state != StateActive {
		return nil
	}
	if im.check != nil && !im.check() {
		return nil
	}
	return im
}

func (f *Fiber) load() {
	if f.uid < 0 || f.runtime == nil || f.state == StateDisposed {
		return
	}
	f.err = nil
	f.setState(StateLoading)
	f.committed = make(map[string]*impl, len(f.store))
	for name, im := range f.store {
		f.committed[name] = im
		if im.ctx.fiber != f {
			im.dependents[f] = struct{}{}
		}
	}

	_, err := f.effect("ctx.plugin()", func() (Disposer, error) {
		config, err := f.resolveConfig()
		if err != nil {
			return nil, err
		}
		return nil, f.runtime.plugin.Apply(f.ctx, config)
	})
	if err != nil {
		f.err = err
		for _, im := range f.committed {
			delete(im.dependents, f)
		}
		f.committed = nil
		f.store = map[string]*impl{}
		f.setState(StateFailed)
		f.ctx.Error(err)
		return
	}
	f.setState(StateActive)
}

func (f *Fiber) resolveConfig() (any, error) {
	config := f.config
	if f.ctx.events.has("internal/config") {
		config = f.ctx.Waterfall("internal/config", []any{f}, func() any { return config })
	}
	if f.runtime.plugin.Config != nil {
		return f.runtime.plugin.Config(config)
	}
	return config, nil
}

func (f *Fiber) unload(dispose bool) {
	if f.uid < 0 || f.state == StateDisposed || f.state == StateUnloading {
		return
	}
	f.setState(StateUnloading)

	// Dependents unload first, while the values this fiber provides are still
	// reachable from their teardown code.
	dependents := map[*Fiber]struct{}{}
	for im := range f.provisions {
		for dep := range im.dependents {
			if dep != f {
				dependents[dep] = struct{}{}
			}
		}
	}
	for dep := range dependents {
		if dep.state == StateActive || dep.state == StateLoading {
			dep.refresh()
		}
	}

	for i := len(f.effects) - 1; i >= 0; i-- {
		_ = f.effects[i].dispose()
	}
	f.effects = nil
	f.currentEffect = nil

	for _, im := range f.committed {
		delete(im.dependents, f)
	}
	f.committed = nil
	f.store = map[string]*impl{}

	if dispose {
		f.setState(StateDisposed)
	} else {
		f.setState(StatePending)
	}
}

func (f *Fiber) disposeNow() {
	if f == nil || f.uid <= 0 || f.state == StateDisposed {
		return
	}
	f.unload(true)
	f.uid = -1
	f.ctx.registry.remove(f)
	f.ctx.events.Emit("internal/plugin", f)
}

// Dispose unmounts the plugin and removes it from the registry.
func (f *Fiber) Dispose() {
	if f == nil || f.uid <= 0 {
		return
	}
	f.ctx.engine.run(f.disposeNow)
}

// Wait blocks until pending lifecycle work has settled. It must not be called
// from within a plugin's Apply function.
func (f *Fiber) Wait() error {
	f.ctx.engine.wait()
	return f.err
}

// Update replaces the config and reloads the plugin.
func (f *Fiber) Update(config any) {
	if !f.usable() {
		return
	}
	if f.ctx.events.has("internal/commit") {
		f.ctx.events.Emit("internal/commit", f, config)
	}
	f.config = config
	f.err = nil
	f.revision++
	f.ctx.engine.run(f.refresh)
}

func (f *Fiber) injectNames() []string {
	names := make([]string, 0, len(f.inject))
	for name := range f.inject {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}
