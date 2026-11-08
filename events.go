package cordis

import (
	"errors"
	"fmt"
	"sync"
)

// Listener observes an event. Its result is used by the bail and serial modes.
type Listener func(*Context, ...any) any

// WaterfallListener wraps an event. Calling next runs the downstream
// listeners; returning without calling it short-circuits.
type WaterfallListener func(*Context, []any, func() any) any

// EventOption configures a listener registration.
type EventOption struct {
	Prepend bool
	Global  bool
}

type hook struct {
	ctx       *Context
	listener  Listener
	waterfall WaterfallListener
	prepend   bool
	global    bool
	removed   bool
}

type EventsService struct {
	ctx   *Context
	mu    sync.RWMutex
	hooks map[string][]*hook
}

// On registers a listener as an effect of the current fiber.
func (c *Context) On(name string, fn Listener, options ...EventOption) (Disposer, error) {
	return c.addHook(name, &hook{listener: fn}, options)
}

// OnWaterfall registers a middleware listener for the waterfall mode.
func (c *Context) OnWaterfall(name string, fn WaterfallListener, options ...EventOption) (Disposer, error) {
	return c.addHook(name, &hook{waterfall: fn}, options)
}

// Once registers a listener that removes itself before its first call.
func (c *Context) Once(name string, fn Listener, options ...EventOption) (Disposer, error) {
	var dispose Disposer
	dispose, err := c.On(name, func(ctx *Context, args ...any) any {
		if dispose != nil {
			_ = dispose()
		}
		return fn(ctx, args...)
	}, options...)
	return dispose, err
}

func (c *Context) addHook(name string, h *hook, options []EventOption) (Disposer, error) {
	for _, option := range options {
		h.prepend = h.prepend || option.Prepend
		h.global = h.global || option.Global
	}
	return c.fiber.effect("ctx.on("+name+")", func() (Disposer, error) {
		h.ctx = c
		c.events.mu.Lock()
		list := c.events.hooks[name]
		if h.prepend {
			list = append([]*hook{h}, list...)
		} else {
			list = append(list, h)
		}
		c.events.hooks[name] = list
		c.events.mu.Unlock()
		return func() error {
			c.events.mu.Lock()
			h.removed = true
			list := c.events.hooks[name]
			for i, candidate := range list {
				if candidate == h {
					list = append(list[:i], list[i+1:]...)
					break
				}
			}
			if len(list) == 0 {
				delete(c.events.hooks, name)
			} else {
				c.events.hooks[name] = list
			}
			c.events.mu.Unlock()
			return nil
		}, nil
	})
}

func (s *EventsService) Emit(name string, args ...any) {
	s.ctx.Emit(name, args...)
}

func (s *EventsService) snapshot(name string) []*hook {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return append([]*hook(nil), s.hooks[name]...)
}

func (s *EventsService) has(name string) bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return len(s.hooks[name]) > 0
}

// Emit dispatches to every listener in registration order.
func (c *Context) Emit(name string, args ...any) {
	for _, h := range c.events.snapshot(name) {
		if h.removed || h.listener == nil {
			continue
		}
		h.listener(h.ctx, args...)
	}
}

// Parallel dispatches to every listener concurrently and joins their errors.
func (c *Context) Parallel(name string, args ...any) error {
	hooks := c.events.snapshot(name)
	var wg sync.WaitGroup
	errs := make([]error, len(hooks))
	for i, h := range hooks {
		if h.removed || h.listener == nil {
			continue
		}
		wg.Add(1)
		go func() {
			defer wg.Done()
			defer func() {
				if reason := recover(); reason != nil {
					errs[i] = fmt.Errorf("listener panic: %v", reason)
				}
			}()
			if result := h.listener(h.ctx, args...); result != nil {
				if err, ok := result.(error); ok {
					errs[i] = err
				}
			}
		}()
	}
	wg.Wait()
	return errors.Join(errs...)
}

// Serial calls listeners in order and returns the first bail value.
func (c *Context) Serial(name string, args ...any) any {
	for _, h := range c.events.snapshot(name) {
		if h.removed || h.listener == nil {
			continue
		}
		if result := h.listener(h.ctx, args...); isBailed(result) {
			return result
		}
	}
	return nil
}

// Bail calls listeners in order and stops at the first bail value.
func (c *Context) Bail(name string, args ...any) any {
	for _, h := range c.events.snapshot(name) {
		if h.removed || h.listener == nil {
			continue
		}
		if result := h.listener(h.ctx, args...); isBailed(result) {
			return result
		}
	}
	return nil
}

// Waterfall runs middleware around next. Each listener may call next to
// delegate, wrap the result, or return early.
func (c *Context) Waterfall(name string, args []any, next func() any) any {
	hooks := c.events.snapshot(name)
	index := 0
	var dispatch func() any
	dispatch = func() any {
		for index < len(hooks) && hooks[index].removed {
			index++
		}
		if index >= len(hooks) {
			return next()
		}
		h := hooks[index]
		index++
		called := false
		delegate := func() any {
			if called {
				panic("next() called multiple times")
			}
			called = true
			return dispatch()
		}
		if h.waterfall != nil {
			return h.waterfall(h.ctx, args, delegate)
		}
		h.listener(h.ctx, args...)
		return delegate()
	}
	return dispatch()
}

func isBailed(value any) bool {
	switch v := value.(type) {
	case nil:
		return false
	case bool:
		return v
	default:
		return true
	}
}
