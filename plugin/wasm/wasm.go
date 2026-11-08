// Package wasm mounts Go WebAssembly components as cordis plugins.
//
// Guests are built with:
//
//	GOOS=wasip1 GOARCH=wasm go build -buildmode=c-shared -o plugin.wasm ./guest
//
// and export cordis_alloc, cordis_apply, cordis_event and cordis_dispose.
// The host module "cordis" imports log, subscribe and emit.
package wasm

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/tetratelabs/wazero"
	"github.com/tetratelabs/wazero/api"
	"github.com/tetratelabs/wazero/imports/wasi_snapshot_preview1"

	"gocordis"
	"gocordis/fs"
)

// Plugin is the component users mount. Its config is a Config or a mapping
// with path, watch, settings, timeout and memoryPages.
var Plugin = &cordis.Plugin{Name: "wasm", Apply: apply}

// Config controls one WASM component.
type Config struct {
	Path        string
	Watch       bool
	Settings    any
	Timeout     time.Duration
	MemoryPages uint32
}

const (
	defaultTimeout     = 5 * time.Second
	defaultMemoryPages = 2048 // 128 MiB
)

func apply(ctx *cordis.Context, raw any) error {
	config, err := decodeConfig(raw)
	if err != nil {
		return err
	}
	path := config.Path
	if !filepath.IsAbs(path) {
		path = filepath.Join(ctx.BaseURL(), path)
	}
	code, err := os.ReadFile(path)
	if err != nil {
		return err
	}

	instance, err := newInstance(ctx, filepath.Base(path), code, config, raw)
	if err != nil {
		return err
	}
	if err := instance.start(); err != nil {
		_ = instance.close()
		return err
	}
	if _, err := ctx.Effect("wasm.module()", func() (cordis.Disposer, error) {
		return instance.close, nil
	}); err != nil {
		_ = instance.close()
		return err
	}

	if config.Watch {
		if err := instance.watch(path, raw); err != nil {
			return err
		}
	}
	return nil
}

func decodeConfig(raw any) (Config, error) {
	switch value := raw.(type) {
	case Config:
		return normalize(value)
	case *Config:
		if value == nil {
			return Config{}, errors.New("wasm: nil config")
		}
		return normalize(*value)
	case map[string]any:
		config := Config{}
		if path, ok := value["path"].(string); ok {
			config.Path = path
		}
		if watch, ok := value["watch"].(bool); ok {
			config.Watch = watch
		}
		if settings, ok := value["settings"]; ok {
			config.Settings = settings
		}
		if settings, ok := value["config"]; ok && config.Settings == nil {
			config.Settings = settings
		}
		if timeout, ok := value["timeout"]; ok {
			config.Timeout = parseDuration(timeout)
		}
		if pages, ok := value["memoryPages"].(int); ok && pages > 0 {
			config.MemoryPages = uint32(pages)
		}
		if config.Settings == nil {
			settings := make(map[string]any, len(value))
			for key, item := range value {
				switch key {
				case "path", "watch", "timeout", "memoryPages", "settings", "config":
					continue
				}
				settings[key] = item
			}
			if len(settings) > 0 {
				config.Settings = settings
			}
		}
		return normalize(config)
	default:
		data, err := json.Marshal(raw)
		if err != nil {
			return Config{}, err
		}
		var config Config
		if err := json.Unmarshal(data, &config); err != nil {
			return Config{}, err
		}
		return normalize(config)
	}
}

func normalize(config Config) (Config, error) {
	if config.Path == "" {
		return Config{}, errors.New("wasm: config.path is required")
	}
	if config.Timeout <= 0 {
		config.Timeout = defaultTimeout
	}
	if config.MemoryPages == 0 {
		config.MemoryPages = defaultMemoryPages
	}
	return config, nil
}

func parseDuration(value any) time.Duration {
	switch typed := value.(type) {
	case time.Duration:
		return typed
	case int:
		return time.Duration(typed) * time.Millisecond
	case int64:
		return time.Duration(typed) * time.Millisecond
	case float64:
		return time.Duration(typed * float64(time.Millisecond))
	case string:
		if duration, err := time.ParseDuration(typed); err == nil {
			return duration
		}
	}
	return 0
}

type instance struct {
	ctx      *cordis.Context
	name     string
	config   Config
	settings []byte
	raw      any

	runtime wazero.Runtime
	module  api.Module
	alloc   api.Function
	applyFn api.Function
	eventFn api.Function
	dispose api.Function

	mu      sync.Mutex
	closed  sync.Once
	calling bool
	pending []pendingEvent

	subMu         sync.Mutex
	subscriptions map[string]bool
}

type pendingEvent struct {
	name    string
	payload []byte
}

func newInstance(ctx *cordis.Context, name string, code []byte, config Config, raw any) (*instance, error) {
	settings, err := json.Marshal(config.Settings)
	if err != nil {
		return nil, err
	}

	runtime := wazero.NewRuntimeWithConfig(context.Background(),
		wazero.NewRuntimeConfig().
			WithMemoryLimitPages(config.MemoryPages).
			WithCloseOnContextDone(true))
	wasi_snapshot_preview1.MustInstantiate(context.Background(), runtime)

	inst := &instance{
		ctx:           ctx,
		name:          "wasm:" + name,
		config:        config,
		settings:      settings,
		raw:           raw,
		runtime:       runtime,
		subscriptions: map[string]bool{},
	}

	if _, err := runtime.NewHostModuleBuilder("cordis").
		NewFunctionBuilder().WithFunc(inst.hostLog).Export("log").
		NewFunctionBuilder().WithFunc(inst.hostSubscribe).Export("subscribe").
		NewFunctionBuilder().WithFunc(inst.hostEmit).Export("emit").
		Instantiate(context.Background()); err != nil {
		_ = runtime.Close(context.Background())
		return nil, err
	}

	module, err := runtime.InstantiateWithConfig(context.Background(), code,
		wazero.NewModuleConfig().WithStartFunctions())
	if err != nil {
		_ = runtime.Close(context.Background())
		return nil, err
	}
	inst.module = module

	callCtx, cancel := context.WithTimeout(context.Background(), config.Timeout)
	defer cancel()
	if init := module.ExportedFunction("_initialize"); init != nil {
		if _, err := init.Call(callCtx); err != nil {
			_ = runtime.Close(context.Background())
			return nil, err
		}
	}

	for symbol, target := range map[string]*api.Function{
		"cordis_alloc":   &inst.alloc,
		"cordis_apply":   &inst.applyFn,
		"cordis_event":   &inst.eventFn,
		"cordis_dispose": &inst.dispose,
	} {
		function := module.ExportedFunction(symbol)
		if function == nil {
			_ = runtime.Close(context.Background())
			return nil, fmt.Errorf("wasm: guest does not export %s", symbol)
		}
		*target = function
	}
	return inst, nil
}

func (i *instance) start() error {
	i.mu.Lock()
	if i.calling {
		i.mu.Unlock()
		return errors.New("wasm: reentrant apply")
	}
	i.calling = true
	err := i.callApply()
	err = i.drain(err)
	i.calling = false
	i.mu.Unlock()
	return err
}

// drain delivers events emitted while the module was executing. Re-entering a
// module call synchronously would deadlock, so those events are queued.
func (i *instance) drain(err error) error {
	if err != nil {
		i.pending = nil
		return err
	}
	for len(i.pending) > 0 {
		next := i.pending[0]
		i.pending = i.pending[1:]
		if eventErr := i.callEventLocked(next.name, next.payload); eventErr != nil {
			i.pending = nil
			return eventErr
		}
	}
	return nil
}

func (i *instance) callApply() error {
	callCtx, cancel := context.WithTimeout(context.Background(), i.config.Timeout)
	defer cancel()
	ptr, err := i.allocate(callCtx, i.settings)
	if err != nil {
		return err
	}
	result, err := i.applyFn.Call(callCtx, uint64(ptr), uint64(len(i.settings)))
	if err != nil {
		return err
	}
	if len(result) == 0 {
		return errors.New("wasm: cordis_apply returned no status")
	}
	if result[0] != 0 {
		return fmt.Errorf("wasm: cordis_apply returned status %d", result[0])
	}
	return nil
}

func (i *instance) allocate(ctx context.Context, data []byte) (uint32, error) {
	if len(data) == 0 {
		return 0, nil
	}
	result, err := i.alloc.Call(ctx, uint64(len(data)))
	if err != nil {
		return 0, err
	}
	if len(result) == 0 {
		return 0, errors.New("wasm: cordis_alloc returned no pointer")
	}
	ptr := uint32(result[0])
	if !i.module.Memory().Write(ptr, data) {
		return 0, errors.New("wasm: config does not fit guest memory")
	}
	return ptr, nil
}

func (i *instance) callEvent(name string, payload []byte) error {
	i.mu.Lock()
	if i.calling {
		i.pending = append(i.pending, pendingEvent{name: name, payload: payload})
		i.mu.Unlock()
		return nil
	}
	i.calling = true
	err := i.callEventLocked(name, payload)
	err = i.drain(err)
	i.calling = false
	i.mu.Unlock()
	return err
}

func (i *instance) callEventLocked(name string, payload []byte) error {
	callCtx, cancel := context.WithTimeout(context.Background(), i.config.Timeout)
	defer cancel()
	data := append(append([]byte(nil), name...), payload...)
	ptr, err := i.allocate(callCtx, data)
	if err != nil {
		return err
	}
	_, err = i.eventFn.Call(callCtx,
		uint64(ptr), uint64(len(name)),
		uint64(ptr)+uint64(len(name)), uint64(len(payload)))
	return err
}

func (i *instance) close() error {
	var err error
	i.mu.Lock()
	i.closed.Do(func() {
		if i.dispose != nil {
			callCtx, cancel := context.WithTimeout(context.Background(), i.config.Timeout)
			_, callErr := i.dispose.Call(callCtx)
			cancel()
			if callErr != nil {
				err = callErr
			}
		}
		if closeErr := i.runtime.Close(context.Background()); err == nil {
			err = closeErr
		}
	})
	i.mu.Unlock()
	return err
}

func (i *instance) watch(path string, raw any) error {
	fsService, ok := i.ctx.Get(fs.Key)
	if !ok {
		return errors.New("wasm: watch requires the fs service")
	}
	watcher, err := fsService.Watch(path)
	if err != nil {
		return err
	}
	debounced := fs.Debounce(200*time.Millisecond, func(string) {
		i.ctx.Fiber().Update(raw)
	})
	_, err = i.ctx.Effect("wasm.watch()", func() (cordis.Disposer, error) {
		go func() {
			for range watcher.Events {
				debounced(path)
			}
		}()
		return watcher.Close, nil
	})
	if err != nil {
		_ = watcher.Close()
		return err
	}
	return nil
}

func (i *instance) hostLog(_ context.Context, module api.Module, level, ptr, size uint32) {
	data, ok := module.Memory().Read(ptr, size)
	if !ok {
		return
	}
	logger := i.ctx.Logger(i.name)
	switch level {
	case 0:
		logger.Error(string(data))
	case 1:
		logger.Warn(string(data))
	case 3:
		logger.Debug(string(data))
	default:
		logger.Info(string(data))
	}
}

func (i *instance) hostSubscribe(_ context.Context, module api.Module, ptr, size uint32) {
	data, ok := module.Memory().Read(ptr, size)
	if !ok || len(data) == 0 {
		return
	}
	name := string(data)
	i.subMu.Lock()
	if i.subscriptions[name] {
		i.subMu.Unlock()
		return
	}
	i.subscriptions[name] = true
	i.subMu.Unlock()

	listener := func(_ *cordis.Context, args ...any) any {
		if err := i.callEvent(name, encodeArgs(args)); err != nil {
			i.ctx.Logger(i.name).Warn(err)
		}
		return nil
	}
	if _, err := i.ctx.On(name, listener); err != nil {
		i.ctx.Logger(i.name).Warn(err)
	}
}

func (i *instance) hostEmit(_ context.Context, module api.Module, namePtr, nameLen, payloadPtr, payloadLen uint32) {
	name, ok := module.Memory().Read(namePtr, nameLen)
	if !ok || len(name) == 0 {
		return
	}
	payload, ok := module.Memory().Read(payloadPtr, payloadLen)
	if !ok {
		return
	}
	i.ctx.Emit(string(name), string(payload))
}

func encodeArgs(args []any) []byte {
	if len(args) == 1 {
		switch value := args[0].(type) {
		case string:
			return []byte(value)
		case []byte:
			return value
		}
	}
	data, err := json.Marshal(args)
	if err != nil {
		return nil
	}
	return data
}
