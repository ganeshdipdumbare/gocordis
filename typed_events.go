package cordis

// Event is a typed view over a named emit/bail/serial event.
type Event[T any] struct{ name string }

// NewEvent declares a typed event.
func NewEvent[T any](name string) *Event[T] { return &Event[T]{name: name} }

// Name is the underlying event name.
func (e *Event[T]) Name() string { return e.name }

// On registers a typed listener.
func (e *Event[T]) On(ctx *Context, fn func(*Context, T)) (Disposer, error) {
	return ctx.On(e.name, func(c *Context, args ...any) any {
		value, _ := args[0].(T)
		fn(c, value)
		return nil
	})
}

// Emit dispatches the event.
func (e *Event[T]) Emit(ctx *Context, value T) { ctx.Emit(e.name, value) }

// Bail dispatches and returns the first non-nil, non-false result.
func (e *Event[T]) Bail(ctx *Context, value T) any { return ctx.Bail(e.name, value) }

// Serial dispatches and returns the first non-nil, non-false result.
func (e *Event[T]) Serial(ctx *Context, value T) any { return ctx.Serial(e.name, value) }

// Waterfall is a typed middleware event.
type Waterfall[T any] struct{ name string }

// NewWaterfall declares a typed waterfall event.
func NewWaterfall[T any](name string) *Waterfall[T] { return &Waterfall[T]{name: name} }

// Name is the underlying event name.
func (e *Waterfall[T]) Name() string { return e.name }

// On registers middleware. Calling next runs the downstream listeners.
func (e *Waterfall[T]) On(ctx *Context, fn func(*Context, T, func() T) T) (Disposer, error) {
	return ctx.OnWaterfall(e.name, func(c *Context, args []any, next func() any) any {
		value, _ := args[0].(T)
		return fn(c, value, func() T {
			result, _ := next().(T)
			return result
		})
	})
}

// Run dispatches with a terminal handler.
func (e *Waterfall[T]) Run(ctx *Context, value T, next func() T) T {
	result := ctx.Waterfall(e.name, []any{value}, func() any { return next() })
	typed, _ := result.(T)
	return typed
}
