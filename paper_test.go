package cordis

import (
	"errors"
	"fmt"
	"testing"
)

func TestRequireCommittedView(t *testing.T) {
	root := New()
	key := NewKey[*testService]("svc")

	if _, err := root.Require(key); !errors.Is(err, ErrUndeclaredAccess) {
		t.Fatalf("root Require() = %v, want undeclared", err)
	}

	provider := &Plugin{Name: "provider", Apply: func(ctx *Context, _ any) error {
		_, err := ctx.Provide(key, &testService{"value"})
		return err
	}}
	consumer := &Plugin{Name: "consumer", Inject: []string{"svc"}, Apply: func(ctx *Context, _ any) error {
		if _, err := ctx.Require(key); err != nil {
			return err
		}
		return nil
	}}

	root.Plugin(provider, nil)
	pending := root.Plugin(consumer, nil)
	if _, err := pending.Context().Require(key); !errors.Is(err, ErrInactiveAccess) && pending.State() != StateActive {
		t.Fatalf("pending Require() = %v, want active or inactive access", err)
	}
	if err := pending.Wait(); err != nil {
		t.Fatal(err)
	}
	if _, err := pending.Context().Require(key); err != nil {
		t.Fatalf("active Require() = %v", err)
	}
}

func TestEffectSeqIsLIFOAndGuarded(t *testing.T) {
	root := New()
	var order []int
	dispose, err := root.EffectSeq("seq", func(yield func(Disposer) bool) {
		for i := 1; i <= 3; i++ {
			value := i
			if !yield(func() error {
				order = append(order, value)
				return nil
			}) {
				return
			}
		}
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := dispose(); err != nil {
		t.Fatal(err)
	}
	if fmt.Sprint(order) != fmt.Sprint([]int{3, 2, 1}) {
		t.Fatalf("inverse order = %v, want [3 2 1]", order)
	}
}

func TestInterceptMergesAndOverrides(t *testing.T) {
	root := New()
	base := root.Intercept("svc", map[string]any{"a": 1, "b": 1})
	child := base.Intercept("svc", map[string]any{"b": 2, "c": 3})

	value, ok := child.InterceptValue("svc")
	if !ok {
		t.Fatal("no intercept value")
	}
	mapping := value.(map[string]any)
	if mapping["a"] != 1 || mapping["b"] != 2 || mapping["c"] != 3 {
		t.Fatalf("merged intercept = %v", mapping)
	}

	if override, _ := root.Intercept("svc", "plain").InterceptValue("svc"); override != "plain" {
		t.Fatalf("non-map override = %v", override)
	}
}
