package cordis

import "testing"

func TestPluginMountsInIsolatedRealm(t *testing.T) {
	root := New()
	key := NewKey[string]("svc")
	if _, err := root.Provide(key, "root"); err != nil {
		t.Fatal(err)
	}
	isolated := root.Isolate("svc", "tenant")
	if _, err := isolated.Provide(key, "isolated"); err != nil {
		t.Fatal(err)
	}

	got := ""
	isolated.Plugin(&Plugin{Name: "probe", Apply: func(ctx *Context, _ any) error {
		got, _ = ctx.Get(key)
		return nil
	}}, nil)
	if got != "isolated" {
		t.Fatalf("mounted plugin resolved %q, want isolated", got)
	}
}

func TestChildPluginDisposedWithParent(t *testing.T) {
	root := New()
	disposed := false
	parent := &Plugin{Name: "parent", Apply: func(ctx *Context, _ any) error {
		ctx.Plugin(&Plugin{Name: "child", Apply: func(child *Context, _ any) error {
			_, err := child.Effect("marker", func() (Disposer, error) {
				return func() error { disposed = true; return nil }, nil
			})
			return err
		}}, nil)
		return nil
	}}

	fiber := root.Plugin(parent, nil)
	fiber.Dispose()
	if !disposed {
		t.Fatal("child survived parent disposal")
	}
}
