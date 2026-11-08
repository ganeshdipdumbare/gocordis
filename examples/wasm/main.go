package main

import (
	"fmt"
	"os"
	"path/filepath"

	"gocordis"
	"gocordis/loggerconsole"
	"gocordis/plugin/wasm"
)

func main() {
	path := "plugin.wasm"
	if _, err := os.Stat(path); err != nil {
		path = filepath.Join("examples", "wasm", "plugin.wasm")
	}
	if len(os.Args) > 1 {
		path = os.Args[1]
	}

	root := cordis.New()
	if _, err := loggerconsole.New(root, loggerconsole.Config{}); err != nil {
		panic(err)
	}
	if _, err := root.On("pong", func(ctx *cordis.Context, args ...any) any {
		ctx.Logger("wasm").Info("pong %v", args)
		return nil
	}); err != nil {
		panic(err)
	}
	fiber := root.Plugin(wasm.Plugin, map[string]any{
		"path":     path,
		"settings": map[string]any{"greeting": "hello"},
	})
	if err := fiber.Wait(); err != nil {
		panic(err)
	}

	root.Emit("ping", "from host")
	root.Dispose()
	fmt.Println("done")
}
