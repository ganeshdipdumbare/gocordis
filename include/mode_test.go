package include

import (
	"os"
	"path/filepath"
	"testing"

	"gocordis/loader"
)

func TestWriteBackPreservesMode(t *testing.T) {
	root, l, _ := testSetup(t)
	path := filepath.Join(t.TempDir(), "cordis.yml")
	if err := os.WriteFile(path, []byte("- id: a\n  name: hello\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(path, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := New(root, Config{Path: path}); err != nil {
		t.Fatal(err)
	}
	if _, err := l.Create("", loader.EntryOptions{ID: "b", Name: "hello"}); err != nil {
		t.Fatal(err)
	}

	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("mode = %04o, want 0600", info.Mode().Perm())
	}
}

func TestInitialFileIsPrivate(t *testing.T) {
	root, _, _ := testSetup(t)
	path := filepath.Join(t.TempDir(), "cordis.yml")
	if _, err := New(root, Config{
		Path:    path,
		Initial: []loader.EntryOptions{{ID: "a", Name: "hello"}},
	}); err != nil {
		t.Fatal(err)
	}

	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("mode = %04o, want 0600", info.Mode().Perm())
	}
}
