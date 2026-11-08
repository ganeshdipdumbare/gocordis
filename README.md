# gocordis

A Go port of [Cordis](https://github.com/cordiverse/cordis), the framework from
*A Programming Paradigm for Spatiotemporal Composability*
([arXiv:2608.25512](https://arxiv.org/abs/2608.25512)). Go 1.27 required
(generic methods).

```go
root := cordis.New()
root.Plugin(&cordis.Plugin{
    Name:   "greeter",
    Inject: []string{"config"},
    Apply: func(ctx *cordis.Context, _ any) error {
        cfg := ctx.MustGet(configKey)
        _, err := ctx.On("tick", func(ctx *cordis.Context, _ ...any) any {
            ctx.Logger("greeter").Info("hello %s", cfg.Name)
            return nil
        })
        return err
    },
}, nil)
```

## Packages

| Package | Status |
|---|---|
| `cordis` | context, fibers, revertible effects (single and iterator), inject gating, typed keys, committed-view access, isolation, merged interception, five event modes, typed events, registry, logger |
| `loader` | entry tree, groups, catalog resolver, `url`/`name`, isolate/intercept, `!!js` expressions, runtime `Create`/`Remove`/`Update`, moves, change notifications |
| `include` | YAML/JSON source, initial write, patches, runtime journal, three-way reconciliation (file wins), atomic write-back, stable anonymous ids, watch |
| `fs` | recursive watcher, glob filters, debounce |
| `hmr` | config-level reload callbacks |
| `pkgmanager` | `go get` / `go mod tidy` adapter |
| `plugin/wasm` | Go `wasip1` components via wazero, JSON-free host ABI, memory/time limits, watch reload |
| `timer` | timeout, interval, debounce, throttle as effects |
| `loggerconsole` | console exporter |

Core has no third-party dependencies. The loader uses `yaml.v3` and
`expr-lang`; `fs` and `include` use `fsnotify` and `doublestar`.

## Paper mapping

| Paper | Implementation |
|---|---|
| `Γ∞`, the context | `*cordis.Context`; `Extend` derives child views |
| revertible effects `ctx.effect` | `Context.Effect`, `Context.EffectSeq` (LIFO, target guard, nested children) |
| `Σ`, `Σiso`, `Σinter` | reflect store, `isolateScope`, `interceptNode` |
| `get(k)`, `set(k,v)`, `isolate`, `intercept` | `Get`, `Provide`, `Isolate`, `Intercept` (metadata maps merge) |
| fiber `⟨d,p,e,π,σ,τ,θ⟩` | `cordis.Fiber`, six states, `inject`, `committed` |
| `ctx.use` | `Context.Use` / `Context.Plugin` |
| committed-view access (Algorithm 6) | `Context.Require` with `ErrInactiveAccess` / `ErrUndeclaredAccess` |
| target digest (provider identity) | epoch = revision + binding uid + provider fiber uid |
| entry / configuration tree | `loader.EntryOptions`, groups, `Source` |
| reconciliation / write-back | `include.reconcile`, loader change journal, atomic file writes |
| HMR | `hmr` config reload; compiled Go code requires a restart |

## Quickstart

```sh
go test ./...
go test -race ./...
go run ./examples/counter
cd examples/loader && go run .
GOOS=wasip1 GOARCH=wasm go build -buildmode=c-shared -o examples/wasm/plugin.wasm ./examples/wasm/guest
go run ./examples/wasm
```

Use `go fmt ./...`, not the system `gofmt`: the Homebrew symlink on this
machine is Go 1.26 and cannot parse generic methods. The `go` binary switches
to 1.27 through `GOTOOLCHAIN=auto` and the `go` directive in `go.mod`.

## WASM components

`plugin/wasm` mounts a Go WebAssembly reactor as an ordinary cordis plugin.
Build the guest with the Go toolchain:

```sh
GOOS=wasip1 GOARCH=wasm go build -buildmode=c-shared -o plugin.wasm ./guest
```

Guest ABI:

| Direction | Symbol | Meaning |
|---|---|---|
| host import | `cordis.log(level, ptr, len)` | 0 error, 1 warn, 2 info, 3 debug |
| host import | `cordis.subscribe(ptr, len)` | receive the named cordis event |
| host import | `cordis.emit(namePtr, nameLen, payloadPtr, payloadLen)` | emit a cordis event |
| guest export | `cordis_alloc(size) uint32` | allocate a guest buffer |
| guest export | `cordis_apply(configPtr, configLen) uint32` | run once per mount, 0 = ok |
| guest export | `cordis_event(namePtr, nameLen, payloadPtr, payloadLen)` | receive a subscribed event |
| guest export | `cordis_dispose()` | run before the module closes |

`gocordis/plugin/wasm/guest` supplies the imports and buffer helpers for Go
guests. The host gives the module no filesystem, network, environment, stdin or
stdout. Guest memory defaults to 128 MiB and each call has a 5 s timeout
(`memoryPages`, `timeout` in the component config). Config fields are `path`,
`watch`, `settings`, `timeout` and `memoryPages`; `watch: true` rebuilds the
instance through `fiber.Update` when the `.wasm` file changes. That is the
unload/reload story `plugin` cannot provide; it is still a WASM guest contract,
not native Go plugin reload.

## Deviations from Cordis

- No Proxy property access. Services are explicit typed keys
  (`cordis.NewKey[T]`), and `Require` is the explicit point-of-use check that
  the Proxy performs implicitly. There are no shadow/definition-site contexts.
- Effects and lifecycle transitions are synchronous. A plugin mounted from
  inside another plugin's `Apply` starts after the outer `Apply` returns; do
  not call `Wait()` from inside `Apply`.
- A change to a loaded entry's `isolate` or `intercept` rebuilds that entry
  instead of performing the paper's in-place realm reassignment. Observable
  behavior matches; provider identity changes, so dependents reload.
- `hmr` cannot reload compiled Go modules. It re-runs configuration reload
  callbacks; source changes need a process restart.
- `pkgmanager` drives `go get`, not npm/yarn.
- `include` patches are configured programmatically (`include.Config.Patches`),
  not read from a patch section of the YAML file. Patch overlays are applied at
  read time and are deliberately not written back.
- No decorators, callable services, `mixin`, or `accessor`.
- `plugin/wasm` is an additional guest contract built on wazero, not a native
  Go component. It exposes log/subscribe/emit only; typed services and effects
  stay on the Go side.
