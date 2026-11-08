package include

import (
	"os"
	"path/filepath"
	"testing"

	"gocordis"
	"gocordis/loader"
)

func testSetup(t *testing.T) (*cordis.Context, *loader.Loader, *[]any) {
	t.Helper()
	root := cordis.New()
	catalog := loader.NewCatalog()
	applied := &[]any{}
	catalog.Add("hello", &cordis.Plugin{
		Name: "hello",
		Apply: func(_ *cordis.Context, config any) error {
			*applied = append(*applied, config)
			return nil
		},
	})
	l, err := loader.New(root, catalog)
	if err != nil {
		t.Fatal(err)
	}
	return root, l, applied
}

func TestLoadAndReload(t *testing.T) {
	root, _, applied := testSetup(t)
	path := filepath.Join(t.TempDir(), "cordis.yml")
	if err := os.WriteFile(path, []byte("- id: a\n  name: hello\n  config:\n    value: 1\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	include, err := New(root, Config{Path: path})
	if err != nil {
		t.Fatal(err)
	}
	if len(*applied) != 1 {
		t.Fatalf("applied %d plugins, want 1", len(*applied))
	}

	if err := os.WriteFile(path, []byte("- id: a\n  name: hello\n  config:\n    value: 2\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := include.Load(); err != nil {
		t.Fatal(err)
	}
	if len(*applied) != 2 {
		t.Fatalf("applied %d plugins, want 2", len(*applied))
	}
}

func TestInitialWritesMissingFile(t *testing.T) {
	root, _, applied := testSetup(t)
	path := filepath.Join(t.TempDir(), "cordis.yml")

	_, err := New(root, Config{
		Path:    path,
		Initial: []loader.EntryOptions{{ID: "a", Name: "hello"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(*applied) != 1 {
		t.Fatalf("applied %d plugins, want 1", len(*applied))
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("initial file was not written: %v", err)
	}
}

func TestMissingFileWithoutInitialFails(t *testing.T) {
	root, _, _ := testSetup(t)
	_, err := New(root, Config{Path: filepath.Join(t.TempDir(), "missing.yml")})
	if err == nil {
		t.Fatal("expected an error for a missing file")
	}
}
