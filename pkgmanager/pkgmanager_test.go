package pkgmanager

import (
	"os"
	"path/filepath"
	"testing"

	"gocordis"
)

func TestFindsModuleRoot(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module example\n\ngo 1.27.0\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	nested := filepath.Join(dir, "a", "b")
	if err := os.MkdirAll(nested, 0o755); err != nil {
		t.Fatal(err)
	}

	root := cordis.New()
	service, err := New(root, Config{Root: nested})
	if err != nil {
		t.Fatal(err)
	}
	if service.Root() != dir {
		t.Fatalf("root = %s, want %s", service.Root(), dir)
	}
}

func TestMissingModuleFails(t *testing.T) {
	root := cordis.New()
	if _, err := New(root, Config{Root: t.TempDir()}); err == nil {
		t.Fatal("expected an error without go.mod")
	}
}
