package include

import (
	"os"
	"path/filepath"
	"testing"

	"gocordis"
	"gocordis/loader"
)

func TestComponentConfigUpdateWritesBack(t *testing.T) {
	root := cordis.New()
	catalog := loader.NewCatalog()
	catalog.Add("updater", &cordis.Plugin{
		Name: "updater",
		Apply: func(ctx *cordis.Context, _ any) error {
			_, err := ctx.On("change", func(ctx *cordis.Context, _ ...any) any {
				ctx.Fiber().Update(map[string]any{"value": 5})
				return nil
			})
			return err
		},
	})
	l, err := loader.New(root, catalog)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "cordis.yml")
	if err := os.WriteFile(path, []byte("- id: a\n  name: updater\n  config:\n    value: 1\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := New(root, Config{Path: path}); err != nil {
		t.Fatal(err)
	}

	root.Emit("change")
	l.Await()

	entries := readEntries(t, path)
	a := findEntry(entries, "a")
	if a == nil {
		t.Fatalf("entry disappeared: %+v", entries)
	}
	config, ok := a.Config.(map[string]any)
	if !ok || config["value"] != 5 {
		t.Fatalf("commit was not written back: %+v", a.Config)
	}
}

func TestRootDisposeDoesNotDisableEntries(t *testing.T) {
	root, _, _ := testSetup(t)
	path := filepath.Join(t.TempDir(), "cordis.yml")
	if err := os.WriteFile(path, []byte("- id: a\n  name: hello\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := New(root, Config{Path: path}); err != nil {
		t.Fatal(err)
	}
	root.Dispose()

	entries := readEntries(t, path)
	a := findEntry(entries, "a")
	if a == nil {
		t.Fatalf("entry disappeared: %+v", entries)
	}
	if disabled, _ := a.Disabled.(bool); disabled {
		t.Fatalf("shutdown wrote disabled: %+v", a)
	}
}

func TestAnonymousEntriesGetStableIDs(t *testing.T) {
	root, l, _ := testSetup(t)
	path := filepath.Join(t.TempDir(), "cordis.yml")
	if err := os.WriteFile(path, []byte("- name: hello\n  config:\n    value: 1\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := New(root, Config{Path: path}); err != nil {
		t.Fatal(err)
	}
	first := l.Source()
	if len(first) != 1 || first[0].ID == "" {
		t.Fatalf("anonymous entry was not assigned an id: %+v", first)
	}

	if _, err := l.Create("", loader.EntryOptions{ID: "b", Name: "hello"}); err != nil {
		t.Fatal(err)
	}
	entries := readEntries(t, path)
	if findEntry(entries, first[0].ID) == nil {
		t.Fatalf("assigned id was not written: %+v", entries)
	}
}
