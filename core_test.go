package cordis

import (
	"errors"
	"fmt"
	"sync"
	"testing"
)

type testService struct{ name string }

func TestEffectNestingDisposesInsideOut(t *testing.T) {
	root := New()
	var order []string

	dispose, err := root.Effect("outer", func() (Disposer, error) {
		if _, err := root.Effect("inner", func() (Disposer, error) {
			order = append(order, "inner+")
			return func() error {
				order = append(order, "inner-")
				return nil
			}, nil
		}); err != nil {
			return nil, err
		}
		order = append(order, "outer+")
		return func() error {
			order = append(order, "outer-")
			return nil
		}, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := dispose(); err != nil {
		t.Fatal(err)
	}

	want := []string{"inner+", "outer+", "inner-", "outer-"}
	if fmt.Sprint(order) != fmt.Sprint(want) {
		t.Fatalf("dispose order = %v, want %v", order, want)
	}
}

func TestProvideGetAndIsolation(t *testing.T) {
	root := New()
	key := NewKey[*testService]("svc")

	rootValue := &testService{"root"}
	disposeRoot, err := root.Provide(key, rootValue)
	if err != nil {
		t.Fatal(err)
	}

	got, ok := root.Get(key)
	if !ok || got != rootValue {
		t.Fatalf("root lookup = %v, %v", got, ok)
	}

	isolated := root.Isolate("svc", "one")
	if _, ok := isolated.Get(key); ok {
		t.Fatal("isolated realm must not see the default binding")
	}

	isolatedValue := &testService{"isolated"}
	disposeIsolated, err := isolated.Provide(key, isolatedValue)
	if err != nil {
		t.Fatal(err)
	}
	if got, ok := isolated.Get(key); !ok || got != isolatedValue {
		t.Fatalf("isolated lookup = %v, %v", got, ok)
	}
	if got, _ := root.Get(key); got != rootValue {
		t.Fatalf("default binding changed: %v", got)
	}

	if err := disposeIsolated(); err != nil {
		t.Fatal(err)
	}
	if _, ok := isolated.Get(key); ok {
		t.Fatal("isolated binding survived disposal")
	}
	if err := disposeRoot(); err != nil {
		t.Fatal(err)
	}
	if _, ok := root.Get(key); ok {
		t.Fatal("default binding survived disposal")
	}
}

func TestInjectGatingAndDependencyUnload(t *testing.T) {
	root := New()
	key := NewKey[*testService]("svc")

	var applied, unloaded int
	provider := &Plugin{Name: "provider", Apply: func(ctx *Context, _ any) error {
		_, err := ctx.Provide(key, &testService{"provided"})
		return err
	}}
	consumer := &Plugin{
		Name:   "consumer",
		Inject: []string{"svc"},
		Apply: func(ctx *Context, _ any) error {
			if _, ok := ctx.Get(key); !ok {
				return errors.New("injected service is missing")
			}
			applied++
			_, err := ctx.Effect("marker", func() (Disposer, error) {
				return func() error {
					unloaded++
					return nil
				}, nil
			})
			return err
		},
	}

	first := root.Plugin(provider, nil)
	second := root.Plugin(consumer, nil)
	if err := first.Wait(); err != nil {
		t.Fatal(err)
	}
	if err := second.Wait(); err != nil {
		t.Fatal(err)
	}
	if applied != 1 {
		t.Fatalf("consumer applied %d times, want 1", applied)
	}

	first.Dispose()
	if second.State() != StatePending {
		t.Fatalf("consumer state = %s, want pending", second.State())
	}
	if unloaded != 1 {
		t.Fatalf("consumer unloaded %d times, want 1", unloaded)
	}
}

func TestPluginFailure(t *testing.T) {
	root := New()
	broken := errors.New("boom")
	plugin := &Plugin{Name: "broken", Apply: func(*Context, any) error { return broken }}

	fiber := root.Plugin(plugin, nil)
	if fiber.State() != StateFailed {
		t.Fatalf("state = %s, want failed", fiber.State())
	}
	if err := fiber.Wait(); !errors.Is(err, broken) {
		t.Fatalf("Wait() = %v, want %v", err, broken)
	}
}

func TestUpdateReloadsPlugin(t *testing.T) {
	root := New()
	var configs []int
	plugin := &Plugin{Name: "configurable", Apply: func(_ *Context, config any) error {
		configs = append(configs, config.(int))
		return nil
	}}

	fiber := root.Plugin(plugin, 1)
	fiber.Update(2)

	if fmt.Sprint(configs) != fmt.Sprint([]int{1, 2}) {
		t.Fatalf("applied configs = %v, want [1 2]", configs)
	}
}

func TestEventsModes(t *testing.T) {
	root := New()
	key := NewKey[*testService]("svc")
	_ = key

	var order []string
	if _, err := root.On("demo", func(*Context, ...any) any {
		order = append(order, "a")
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := root.On("demo", func(*Context, ...any) any {
		order = append(order, "b")
		return "stop"
	}, EventOption{Prepend: true}); err != nil {
		t.Fatal(err)
	}

	root.Emit("demo")
	if fmt.Sprint(order) != fmt.Sprint([]string{"b", "a"}) {
		t.Fatalf("emit order = %v", order)
	}

	order = nil
	if value := root.Bail("demo"); value != "stop" {
		t.Fatalf("bail value = %v", value)
	}
	if fmt.Sprint(order) != fmt.Sprint([]string{"b"}) {
		t.Fatalf("bail order = %v", order)
	}

	order = nil
	if value := root.Serial("demo"); value != "stop" {
		t.Fatalf("serial value = %v", value)
	}
	if fmt.Sprint(order) != fmt.Sprint([]string{"b"}) {
		t.Fatalf("serial order = %v", order)
	}
}

func TestWaterfallAndDispose(t *testing.T) {
	root := New()
	dispose, err := root.OnWaterfall("wf", func(_ *Context, _ []any, next func() any) any {
		return "(" + fmt.Sprint(next()) + ")"
	})
	if err != nil {
		t.Fatal(err)
	}
	if got := root.Waterfall("wf", nil, func() any { return "x" }); got != "(x)" {
		t.Fatalf("waterfall = %v", got)
	}
	if err := dispose(); err != nil {
		t.Fatal(err)
	}
	if got := root.Waterfall("wf", nil, func() any { return "x" }); got != "x" {
		t.Fatalf("waterfall after dispose = %v", got)
	}
}

func TestParallelJoinsErrors(t *testing.T) {
	root := New()
	boom := errors.New("boom")
	var mu sync.Mutex
	var calls int
	if _, err := root.On("par", func(*Context, ...any) any {
		mu.Lock()
		calls++
		mu.Unlock()
		return boom
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := root.On("par", func(*Context, ...any) any {
		mu.Lock()
		calls++
		mu.Unlock()
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if err := root.Parallel("par"); !errors.Is(err, boom) {
		t.Fatalf("Parallel() = %v, want boom", err)
	}
	if calls != 2 {
		t.Fatalf("calls = %d, want 2", calls)
	}
}

func TestOnce(t *testing.T) {
	root := New()
	var calls int
	if _, err := root.Once("once", func(*Context, ...any) any {
		calls++
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	root.Emit("once")
	root.Emit("once")
	if calls != 1 {
		t.Fatalf("calls = %d, want 1", calls)
	}
}

func TestLoggerBuffer(t *testing.T) {
	root := New()
	root.Logger("test").Info("hello %s", "world")
	buffer := root.logger.Buffer()
	if len(buffer) != 1 {
		t.Fatalf("buffer length = %d, want 1", len(buffer))
	}
	message := buffer[0]
	if message.Name != "test" || message.Type != "info" {
		t.Fatalf("message = %+v", message)
	}
	if got := formatMessage(message.Args); got != "hello world" {
		t.Fatalf("formatted = %q", got)
	}
}
