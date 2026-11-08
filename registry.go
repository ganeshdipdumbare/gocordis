package cordis

import (
	"fmt"
	"sync"
)

// Plugin describes a mountable unit. Apply runs once per mount; Inject lists
// the service names that must be active before Apply runs.
type Plugin struct {
	Name   string
	Inject []string
	Config func(any) (any, error)
	Apply  func(*Context, any) error
}

// Runtime is the shared registration of one plugin definition.
type Runtime struct {
	plugin *Plugin
	fibers []*Fiber
}

// RegistryService tracks plugin definitions and their mounted fibers.
type RegistryService struct {
	ctx *Context
	mu  sync.Mutex

	runtimes map[*Plugin]*Runtime
	fibers   []*Fiber
	uid      int64
	implUID  int64
}

// Plugin mounts a plugin under the registry's root context.
func (r *RegistryService) Plugin(p *Plugin, config any) *Fiber {
	return r.PluginAt(r.ctx, p, config)
}

// PluginAt mounts a plugin under parent, so the fiber inherits parent's
// isolation, interception, and lifecycle ownership.
func (r *RegistryService) PluginAt(parent *Context, p *Plugin, config any) *Fiber {
	if p == nil || p.Apply == nil {
		panic("cordis: plugin has no Apply function")
	}
	parent.fiber.assertUsable()

	rt := r.runtime(p)

	inject := make(map[string]any, len(p.Inject))
	for _, name := range p.Inject {
		inject[name] = nil
	}

	f := &Fiber{
		ctx:        parent.Extend(),
		parent:     parent.fiber,
		runtime:    rt,
		state:      StatePending,
		config:     config,
		inject:     inject,
		store:      map[string]*impl{},
		provisions: map[*impl]struct{}{},
	}
	f.ctx.fiber = f

	r.mu.Lock()
	f.uid = r.nextUIDLocked()
	rt.fibers = append(rt.fibers, f)
	r.fibers = append(r.fibers, f)
	r.mu.Unlock()

	node := &effectNode{
		label: "ctx.plugin(" + p.Name + ")",
		disposer: func() error {
			f.disposeNow()
			return nil
		},
	}
	pf := parent.fiber
	if pf.currentEffect != nil {
		node.parent = pf.currentEffect
		pf.currentEffect.children = append(pf.currentEffect.children, node)
	} else {
		pf.effects = append(pf.effects, node)
	}

	parent.events.Emit("internal/plugin", f)
	parent.engine.run(f.refresh)
	return f
}

// Replace re-points every mounted fiber of oldPlugin to newPlugin, keeping
// their config and inject declarations. Used by HMR.
func (r *RegistryService) Replace(oldPlugin, newPlugin *Plugin) []*Fiber {
	if oldPlugin == nil || newPlugin == nil {
		return nil
	}
	r.mu.Lock()
	source := r.runtimes[oldPlugin]
	target := r.runtimes[newPlugin]
	if target == nil {
		target = &Runtime{plugin: newPlugin}
		r.runtimes[newPlugin] = target
	}
	var fibers []*Fiber
	if source != nil {
		fibers = append(fibers, source.fibers...)
		delete(r.runtimes, oldPlugin)
	}
	r.mu.Unlock()

	inject := make(map[string]any, len(newPlugin.Inject))
	for _, name := range newPlugin.Inject {
		inject[name] = nil
	}
	for _, f := range fibers {
		r.mu.Lock()
		f.runtime = target
		f.inject = inject
		target.fibers = append(target.fibers, f)
		r.mu.Unlock()
		f.err = nil
		f.revision++
		r.ctx.engine.run(f.refresh)
	}
	return fibers
}

// Has reports whether the plugin has been mounted at least once.
func (r *RegistryService) Has(p *Plugin) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	rt := r.runtimes[p]
	return rt != nil && len(rt.fibers) > 0
}

// Fibers returns a snapshot of mounted fibers.
func (r *RegistryService) Fibers() []*Fiber {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]*Fiber(nil), r.fibers...)
}

func (r *RegistryService) runtime(p *Plugin) *Runtime {
	r.mu.Lock()
	defer r.mu.Unlock()
	rt := r.runtimes[p]
	if rt == nil {
		rt = &Runtime{plugin: p}
		r.runtimes[p] = rt
	}
	return rt
}

func (r *RegistryService) remove(f *Fiber) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if rt := r.runtimes[f.runtime.plugin]; rt != nil {
		for i, candidate := range rt.fibers {
			if candidate == f {
				rt.fibers = append(rt.fibers[:i], rt.fibers[i+1:]...)
				break
			}
		}
	}
	for i, candidate := range r.fibers {
		if candidate == f {
			r.fibers = append(r.fibers[:i], r.fibers[i+1:]...)
			break
		}
	}
}

func (r *RegistryService) notify(name, realm string) {
	r.mu.Lock()
	fibers := append([]*Fiber(nil), r.fibers...)
	r.mu.Unlock()
	for _, f := range fibers {
		if f.uid <= 0 || f.state == StateDisposed {
			continue
		}
		if _, ok := f.inject[name]; !ok {
			continue
		}
		if got, ok := f.ctx.realmOf(name); !ok || got != realm {
			continue
		}
		f.refresh()
	}
}

func (r *RegistryService) nextUIDLocked() int64 {
	r.uid++
	return r.uid
}

func (r *RegistryService) nextImplUID() int64 {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.implUID++
	return r.implUID
}

func (r *RegistryService) String() string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return fmt.Sprintf("Registry<%d plugins, %d fibers>", len(r.runtimes), len(r.fibers))
}
