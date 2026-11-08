package cordis

import (
	"errors"
	"sort"
	"sync"
)

// Disposer releases a registration. Nil means nothing to release.
type Disposer func() error

// ErrInactive is returned when a registration targets a disposed context.
var ErrInactive = errors.New("cannot create effect on inactive context")

// ErrInactiveAccess is returned when a declared coeffect is not committed,
// i.e. the component that declares it has not loaded yet.
var ErrInactiveAccess = errors.New("cannot get required service in inactive context")

// ErrUndeclaredAccess is returned when a service is read without being
// declared through Inject, matching the paper's point-of-use check.
var ErrUndeclaredAccess = errors.New("cannot get service without inject")

// State is the lifecycle state of a Fiber.
type State int

const (
	StatePending State = iota
	StateLoading
	StateActive
	StateFailed
	StateDisposed
	StateUnloading
)

func (s State) String() string {
	switch s {
	case StatePending:
		return "pending"
	case StateLoading:
		return "loading"
	case StateActive:
		return "active"
	case StateFailed:
		return "failed"
	case StateDisposed:
		return "disposed"
	case StateUnloading:
		return "unloading"
	default:
		return "unknown"
	}
}

// effectNode is one reversible registration. Children registered while the
// node executes are disposed before the node itself, in reverse order.
type effectNode struct {
	label    string
	disposer Disposer
	parent   *effectNode
	children []*effectNode
	disposed bool
}

func (n *effectNode) dispose() error {
	if n.disposed {
		return nil
	}
	n.disposed = true
	var errs []error
	for i := len(n.children) - 1; i >= 0; i-- {
		if err := n.children[i].dispose(); err != nil {
			errs = append(errs, err)
		}
	}
	if n.disposer != nil {
		if err := n.disposer(); err != nil {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}

func sortedKeys(m map[string]any) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// engine serializes lifecycle work. A task started from inside another task is
// queued and runs when the current one returns; a task started from outside
// runs to completion before Run returns.
type engine struct {
	mu      sync.Mutex
	cond    *sync.Cond
	queue   []func()
	running bool
}

func newEngine() *engine {
	e := &engine{}
	e.cond = sync.NewCond(&e.mu)
	return e
}

func (e *engine) run(task func()) {
	e.mu.Lock()
	e.queue = append(e.queue, task)
	if e.running {
		e.mu.Unlock()
		return
	}
	e.running = true
	e.mu.Unlock()

	for {
		e.mu.Lock()
		if len(e.queue) == 0 {
			e.running = false
			e.cond.Broadcast()
			e.mu.Unlock()
			return
		}
		next := e.queue[0]
		e.queue = e.queue[1:]
		e.mu.Unlock()

		func() {
			defer func() {
				e.mu.Lock()
				e.cond.Broadcast()
				e.mu.Unlock()
			}()
			next()
		}()
	}
}

// wait blocks until the queue is drained. It must not be called from inside a
// lifecycle task, such as a plugin's Apply function.
func (e *engine) wait() {
	e.mu.Lock()
	for e.running {
		e.cond.Wait()
	}
	e.mu.Unlock()
}
