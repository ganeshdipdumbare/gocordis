package main

import (
	"fmt"
	"sync"

	"gocordis"
	"gocordis/loggerconsole"
)

type Counter struct {
	mu    sync.Mutex
	value int
}

func (c *Counter) Add() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.value++
	return c.value
}

var counterKey = cordis.NewKey[*Counter]("counter")

func main() {
	root := cordis.New()
	if _, err := loggerconsole.New(root, loggerconsole.Config{}); err != nil {
		panic(err)
	}

	provider := &cordis.Plugin{
		Name: "counter",
		Apply: func(ctx *cordis.Context, _ any) error {
			_, err := ctx.Provide(counterKey, &Counter{})
			return err
		},
	}

	reporter := &cordis.Plugin{
		Name:   "reporter",
		Inject: []string{"counter"},
		Apply: func(ctx *cordis.Context, _ any) error {
			counter := ctx.MustGet(counterKey)
			_, err := ctx.On("tick", func(ctx *cordis.Context, _ ...any) any {
				ctx.Logger("reporter").Info("tick %d", counter.Add())
				return nil
			})
			return err
		},
	}

	root.Plugin(provider, nil)
	root.Plugin(reporter, nil)

	for i := 0; i < 3; i++ {
		root.Emit("tick")
	}

	root.Dispose()
	fmt.Println("done")
}
