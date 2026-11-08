package fs

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"gocordis"
)

func TestWatchFile(t *testing.T) {
	root := cordis.New()
	service, err := New(root)
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	watcher, err := service.Watch(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer watcher.Close()

	path := filepath.Join(dir, "a.txt")
	if err := os.WriteFile(path, []byte("hello"), 0o644); err != nil {
		t.Fatal(err)
	}

	select {
	case event := <-watcher.Events:
		if filepath.Clean(event) != filepath.Clean(path) {
			t.Fatalf("event = %s, want %s", event, path)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("no watch event")
	}
}

func TestWatchFilter(t *testing.T) {
	root := cordis.New()
	service, err := New(root)
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	watcher, err := service.Watch(dir, WatchOptions{Filter: []string{"**/*.go"}})
	if err != nil {
		t.Fatal(err)
	}
	defer watcher.Close()

	if err := os.WriteFile(filepath.Join(dir, "ignored.txt"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	select {
	case event := <-watcher.Events:
		t.Fatalf("unexpected event for %s", event)
	case <-time.After(200 * time.Millisecond):
	}
}
