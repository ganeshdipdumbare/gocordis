package include

import (
	"os"
	"path/filepath"
	"testing"

	"gopkg.in/yaml.v3"

	"gocordis"
	"gocordis/loader"
)

func readEntries(t *testing.T, path string) []loader.EntryOptions {
	t.Helper()
	content, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var entries []loader.EntryOptions
	if err := yaml.Unmarshal(content, &entries); err != nil {
		t.Fatal(err)
	}
	return entries
}

func findEntry(entries []loader.EntryOptions, id string) *loader.EntryOptions {
	for index := range entries {
		if entries[index].ID == id {
			return &entries[index]
		}
		if entries[index].Group {
			if found := findEntry(entries[index].Entries, id); found != nil {
				return found
			}
		}
	}
	return nil
}

func TestPatchOverridesButDoesNotLeakIntoFile(t *testing.T) {
	root, l, applied := testSetup(t)
	path := filepath.Join(t.TempDir(), "cordis.yml")
	if err := os.WriteFile(path, []byte("- id: a\n  name: hello\n  config:\n    value: 1\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	patched := any(map[string]any{"value": 2})

	if _, err := New(root, Config{Path: path, Patches: []Patch{{ID: "a", Config: &patched}}}); err != nil {
		t.Fatal(err)
	}
	if len(*applied) != 1 {
		t.Fatalf("applied %d, want 1", len(*applied))
	}
	config := (*applied)[0].(map[string]any)
	if config["value"] != 2 {
		t.Fatalf("patched config = %v", config)
	}

	if _, err := l.Create("", loader.EntryOptions{ID: "b", Name: "hello"}); err != nil {
		t.Fatal(err)
	}
	entries := readEntries(t, path)
	a := findEntry(entries, "a")
	if a == nil || a.Config.(map[string]any)["value"] != 1 {
		t.Fatalf("patch leaked into the file: %+v", a)
	}
	if findEntry(entries, "b") == nil {
		t.Fatalf("runtime entry was not written: %+v", entries)
	}
}

func TestPatchInsertIsNotWrittenBack(t *testing.T) {
	root, l, _ := testSetup(t)
	path := filepath.Join(t.TempDir(), "cordis.yml")
	if err := os.WriteFile(path, []byte("- id: a\n  name: hello\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := New(root, Config{
		Path:    path,
		Patches: []Patch{{Insert: []loader.EntryOptions{{ID: "inserted", Name: "hello"}}}},
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := l.Create("", loader.EntryOptions{ID: "b", Name: "hello"}); err != nil {
		t.Fatal(err)
	}
	entries := readEntries(t, path)
	if findEntry(entries, "inserted") != nil {
		t.Fatalf("patch insert was written: %+v", entries)
	}
	if findEntry(entries, "b") == nil {
		t.Fatalf("runtime entry was not written: %+v", entries)
	}
}

func TestExternalEditWinsOverRuntime(t *testing.T) {
	root, l, applied := testSetup(t)
	path := filepath.Join(t.TempDir(), "cordis.yml")
	if err := os.WriteFile(path, []byte("- id: a\n  name: hello\n  config:\n    value: 1\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	include, err := New(root, Config{Path: path})
	if err != nil {
		t.Fatal(err)
	}

	if err := l.Update("a", loader.EntryOptions{ID: "a", Name: "hello", Config: map[string]any{"value": 2}}); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("- id: a\n  name: hello\n  config:\n    value: 3\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := include.Load(); err != nil {
		t.Fatal(err)
	}

	last := (*applied)[len(*applied)-1].(map[string]any)
	if last["value"] != 3 {
		t.Fatalf("file did not win: %v", last)
	}
}

func TestExternalEditPreservesUnrelatedRuntimeEntry(t *testing.T) {
	root, l, _ := testSetup(t)
	path := filepath.Join(t.TempDir(), "cordis.yml")
	if err := os.WriteFile(path, []byte("- id: a\n  name: hello\n  config:\n    value: 1\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	include, err := New(root, Config{Path: path})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := l.Create("", loader.EntryOptions{ID: "b", Name: "hello", Config: map[string]any{"value": 2}}); err != nil {
		t.Fatal(err)
	}

	if err := os.WriteFile(path, []byte(
		"- id: a\n  name: hello\n  config:\n    value: 9\n"+
			"- id: b\n  name: hello\n  config:\n    value: 2\n",
	), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := include.Load(); err != nil {
		t.Fatal(err)
	}

	source := l.Source()
	a := findEntry(source, "a")
	if a == nil || a.Config.(map[string]any)["value"] != 9 {
		t.Fatalf("file change not applied: %+v", a)
	}
	if findEntry(source, "b") == nil {
		t.Fatalf("runtime entry was dropped: %+v", source)
	}
}

func TestSelfDisableWritesBack(t *testing.T) {
	root := cordis.New()
	catalog := loader.NewCatalog()
	catalog.Add("quitter", &cordis.Plugin{
		Name: "quitter",
		Apply: func(ctx *cordis.Context, _ any) error {
			_, err := ctx.On("quit", func(ctx *cordis.Context, _ ...any) any {
				ctx.Fiber().Dispose()
				return nil
			})
			return err
		},
	})
	if _, err := loader.New(root, catalog); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "cordis.yml")
	if err := os.WriteFile(path, []byte("- id: a\n  name: quitter\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := New(root, Config{Path: path}); err != nil {
		t.Fatal(err)
	}

	root.Emit("quit")
	entries := readEntries(t, path)
	a := findEntry(entries, "a")
	if a == nil {
		t.Fatalf("entry disappeared: %+v", entries)
	}
	if disabled, _ := a.Disabled.(bool); !disabled {
		t.Fatalf("self-disable was not written back: %+v", a)
	}
}
