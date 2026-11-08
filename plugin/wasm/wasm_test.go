package wasm

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"gocordis"
)

func buildGuest(t *testing.T) string {
	t.Helper()
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("cannot locate test file")
	}
	root := filepath.Dir(filepath.Dir(filepath.Dir(file)))
	out := filepath.Join(t.TempDir(), "guest.wasm")
	command := exec.Command("go", "build", "-buildmode=c-shared", "-o", out, "./plugin/wasm/testdata/guest")
	command.Dir = root
	command.Env = append(os.Environ(), "GOOS=wasip1", "GOARCH=wasm", "CGO_ENABLED=0")
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("build guest: %v\n%s", err, output)
	}
	return out
}

type captureExporter struct{ messages []cordis.Message }

func (c *captureExporter) Export(message cordis.Message) {
	c.messages = append(c.messages, message)
}

func TestWasmPluginLifecycle(t *testing.T) {
	path := buildGuest(t)
	root := cordis.New()
	captured := &captureExporter{}
	if _, err := root.AddExporter(captured); err != nil {
		t.Fatal(err)
	}

	received := ""
	if _, err := root.On("pong", func(_ *cordis.Context, args ...any) any {
		if len(args) > 0 {
			received, _ = args[0].(string)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}

	fiber := root.Plugin(Plugin, map[string]any{
		"path":     path,
		"settings": map[string]any{"value": 1},
	})
	if err := fiber.Wait(); err != nil {
		t.Fatal(err)
	}

	root.Emit("ping", "hello")
	if received != "hello" {
		t.Fatalf("pong payload = %q, want hello", received)
	}

	found := false
	for _, message := range captured.messages {
		if strings.Contains(message.String(), `"value":1`) {
			found = true
		}
	}
	if !found {
		t.Fatalf("guest config was not logged: %+v", captured.messages)
	}

	fiber.Dispose()
	if fiber.State() != cordis.StateDisposed {
		t.Fatalf("state = %s, want disposed", fiber.State())
	}
}

func TestWasmMissingPath(t *testing.T) {
	root := cordis.New()
	fiber := root.Plugin(Plugin, map[string]any{})
	if err := fiber.Wait(); err == nil {
		t.Fatal("expected an error for a missing path")
	}
}
