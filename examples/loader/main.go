package main

import (
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"gocordis"
	"gocordis/fs"
	"gocordis/hmr"
	"gocordis/include"
	"gocordis/loader"
	"gocordis/loggerconsole"
)

func main() {
	root := cordis.New()
	if _, err := loggerconsole.New(root, loggerconsole.Config{ShowTime: true}); err != nil {
		panic(err)
	}

	catalog := loader.NewCatalog()
	catalog.Add("greeter", &cordis.Plugin{
		Name: "greeter",
		Apply: func(ctx *cordis.Context, config any) error {
			message := "hello"
			if mapping, ok := config.(map[string]any); ok {
				if value, ok := mapping["message"].(string); ok {
					message = value
				}
			}
			ctx.Logger("greeter").Info("%s", message)
			return nil
		},
	})

	if _, err := loader.New(root, catalog); err != nil {
		panic(err)
	}
	if _, err := fs.New(root); err != nil {
		panic(err)
	}
	reloader, err := hmr.New(root, hmr.Config{Watch: true})
	if err != nil {
		panic(err)
	}

	source, err := include.New(root, include.Config{Path: "cordis.yml"})
	if err != nil {
		panic(err)
	}
	reloader.Handle(source.Load)

	fmt.Println("watching cordis.yml, press Ctrl+C to stop")
	signals := make(chan os.Signal, 1)
	signal.Notify(signals, syscall.SIGINT, syscall.SIGTERM)
	<-signals
	root.Dispose()
}
