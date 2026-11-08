// Package include mounts a loader entry tree from a YAML or JSON file and
// reconciles runtime changes back into the file.
package include

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"sync"
	"time"

	"github.com/fsnotify/fsnotify"
	"gopkg.in/yaml.v3"

	"gocordis"
	"gocordis/loader"
)

// Patch overrides a file entry, or inserts entries under a group.
type Patch struct {
	ID        string                `yaml:"id,omitempty"`
	Insert    []loader.EntryOptions `yaml:"insert,omitempty"`
	Name      *string               `yaml:"name,omitempty"`
	Config    *any                  `yaml:"config,omitempty"`
	Disabled  *any                  `yaml:"disabled,omitempty"`
	Inject    *[]string             `yaml:"inject,omitempty"`
	Intercept *map[string]any       `yaml:"intercept,omitempty"`
	Isolate   *map[string]any       `yaml:"isolate,omitempty"`
}

// Config describes the source file and its overlays.
type Config struct {
	Path       string
	Watch      bool
	Debounce   time.Duration
	Initial    []loader.EntryOptions
	Patches    []Patch
	EnableLogs bool
}

// Include keeps the loader tree in sync with a config file.
type Include struct {
	ctx      *cordis.Context
	loader   *loader.Loader
	config   Config
	path     string
	fileBase []loader.EntryOptions
	applied  []loader.EntryOptions
	content  string
	own      ownership
	counter  int
	mu       sync.Mutex
	watcher  *fsnotify.Watcher
	writing  bool
}

type ownership struct {
	inserted  map[string]bool
	overrides map[string]map[string]bool
}

func (o *ownership) markInserted(entries []loader.EntryOptions) {
	for _, entry := range entries {
		o.inserted[entry.ID] = true
		o.markInserted(entry.Entries)
	}
}

func (o *ownership) mark(key, field string) {
	if o.overrides[key] == nil {
		o.overrides[key] = map[string]bool{}
	}
	o.overrides[key][field] = true
}

// New reads the file and applies it to the loader service already on ctx.
func New(ctx *cordis.Context, config Config) (*Include, error) {
	service, ok := ctx.Get(loader.Key)
	if !ok {
		return nil, errors.New("include requires the loader service")
	}
	path := config.Path
	if !filepath.IsAbs(path) {
		path = filepath.Join(ctx.BaseURL(), path)
	}
	include := &Include{
		ctx:    ctx,
		loader: service,
		config: config,
		path:   path,
	}
	service.SetOnChange(include.onChange)

	if err := include.Load(); err != nil {
		if !os.IsNotExist(err) || config.Initial == nil {
			return nil, err
		}
		if err := include.writeInitial(config.Initial); err != nil {
			return nil, err
		}
		if err := include.Load(); err != nil {
			return nil, err
		}
	}

	if config.Watch {
		if err := include.watch(); err != nil {
			return nil, err
		}
	}
	return include, nil
}

// Path is the resolved config file location.
func (i *Include) Path() string { return i.path }

// Load reads the file and reconciles it with runtime changes.
func (i *Include) Load() error {
	i.mu.Lock()
	defer i.mu.Unlock()
	return i.load()
}

func (i *Include) load() error {
	content, err := os.ReadFile(i.path)
	if err != nil {
		return err
	}
	if i.content != "" && string(content) == i.content {
		return nil
	}

	var theirs []loader.EntryOptions
	if err := yaml.Unmarshal(content, &theirs); err != nil {
		return err
	}
	if theirs == nil {
		return errors.New("config file must be a top-level array")
	}
	theirs = i.assignIDs(theirs)

	var result []loader.EntryOptions
	if i.fileBase == nil {
		result = clone(theirs)
	} else {
		result = reconcile(i.fileBase, i.loader.Source(), theirs)
	}

	patched, own, err := applyPatches(result, i.config.Patches)
	if err != nil {
		return err
	}
	if err := i.loader.Apply(patched); err != nil {
		return err
	}
	i.fileBase = clone(theirs)
	i.applied = clone(patched)
	i.own = own
	i.content = string(content)
	return nil
}

// onChange records a loader mutation and writes it back to the file.
func (i *Include) onChange(change loader.Change) {
	i.mu.Lock()
	defer i.mu.Unlock()
	if i.fileBase == nil {
		return
	}
	if err := i.write(change.Source); err != nil {
		i.ctx.Logger("include").Warn(err)
	}
}

// write persists the runtime tree, reverting values owned by config patches so
// that overlays do not get baked into the file.
func (i *Include) write(runtime []loader.EntryOptions) error {
	if i.writing {
		return nil
	}
	i.writing = true
	defer func() { i.writing = false }()

	file := clone(runtime)
	file = i.stripPatches(file)

	if reflect.DeepEqual(file, i.fileBase) {
		return nil
	}
	data := mustMarshal(file)
	if err := i.atomicWrite(data); err != nil {
		return err
	}
	i.fileBase = clone(file)
	i.applied = clone(runtime)
	i.content = string(data)
	return nil
}

func (i *Include) stripPatches(entries []loader.EntryOptions) []loader.EntryOptions {
	for id := range i.own.inserted {
		entries, _ = loader.RemoveFromTree(entries, id)
	}
	if len(i.own.overrides) == 0 {
		return entries
	}
	flat := loader.Flatten(i.fileBase)
	for id, fields := range i.own.overrides {
		base, ok := flat[id]
		loader.UpdateInTree(entries, id, func(options *loader.EntryOptions) {
			for field := range fields {
				revertField(options, field, base.Options, ok)
			}
		})
	}
	return entries
}

func revertField(target *loader.EntryOptions, field string, base loader.EntryOptions, hasBase bool) {
	switch field {
	case "name":
		target.Name = base.Name
		if !hasBase {
			target.Name = ""
		}
	case "config":
		target.Config = base.Config
		if !hasBase {
			target.Config = nil
		}
	case "disabled":
		target.Disabled = base.Disabled
	case "inject":
		target.Inject = base.Inject
	case "intercept":
		target.Intercept = base.Intercept
	case "isolate":
		target.Isolate = base.Isolate
	}
}

func (i *Include) writeInitial(entries []loader.EntryOptions) error {
	entries = clone(entries)
	assignMissingIDs(entries, "init-", &i.counter)
	return i.atomicWrite(mustMarshal(entries))
}

func (i *Include) atomicWrite(data []byte) error {
	dir := filepath.Dir(i.path)
	temp, err := os.CreateTemp(dir, "."+filepath.Base(i.path)+".*.tmp")
	if err != nil {
		return err
	}
	name := temp.Name()
	defer os.Remove(name)
	if _, err := temp.Write(data); err != nil {
		_ = temp.Close()
		return err
	}
	if err := temp.Close(); err != nil {
		return err
	}
	if err := os.Chmod(name, 0o644); err != nil {
		return err
	}
	return os.Rename(name, i.path)
}

func mustMarshal(entries []loader.EntryOptions) []byte {
	data, _ := yaml.Marshal(entries)
	return data
}

func clone(entries []loader.EntryOptions) []loader.EntryOptions {
	if entries == nil {
		return nil
	}
	result := make([]loader.EntryOptions, len(entries))
	for index, entry := range entries {
		result[index] = entry
		result[index].Entries = clone(entry.Entries)
	}
	return result
}

// reconcile merges three trees. The file wins every conflict, matching the
// paper's configuration reconciliation.
func reconcile(base, runtime, theirs []loader.EntryOptions) []loader.EntryOptions {
	baseFlat := loader.Flatten(base)
	runtimeFlat := loader.Flatten(runtime)
	theirsFlat := loader.Flatten(theirs)

	chosen := map[string]loader.FlatEntry{}
	ids := map[string]bool{}
	for id := range baseFlat {
		ids[id] = true
	}
	for id := range runtimeFlat {
		ids[id] = true
	}
	for id := range theirsFlat {
		ids[id] = true
	}

	for id := range ids {
		baseEntry, hasBase := baseFlat[id]
		runtimeEntry, hasRuntime := runtimeFlat[id]
		theirEntry, hasTheirs := theirsFlat[id]

		runtimeChanged := hasRuntime != hasBase || !reflect.DeepEqual(runtimeEntry, baseEntry)
		fileChanged := hasTheirs != hasBase || !reflect.DeepEqual(theirEntry, baseEntry)

		switch {
		case runtimeChanged && !fileChanged:
			if hasRuntime {
				chosen[id] = runtimeEntry
			}
		default:
			if hasTheirs {
				chosen[id] = theirEntry
			}
		}
	}
	return rebuild(chosen)
}

func rebuild(flat map[string]loader.FlatEntry) []loader.EntryOptions {
	list := make([]loader.FlatEntry, 0, len(flat))
	for _, entry := range flat {
		list = append(list, entry)
	}
	sort.SliceStable(list, func(a, b int) bool {
		da := entryDepth(flat, list[a])
		db := entryDepth(flat, list[b])
		if da != db {
			return da < db
		}
		return list[a].Position < list[b].Position
	})

	var result []loader.EntryOptions
	for _, entry := range list {
		options := entry.Options
		options.Entries = nil
		if entry.Parent == "" {
			result = append(result, options)
			continue
		}
		next, ok := loader.InsertIntoTree(result, entry.Parent, options)
		if !ok {
			result = append(result, options)
			continue
		}
		result = next
	}
	return result
}

func entryDepth(flat map[string]loader.FlatEntry, entry loader.FlatEntry) int {
	depth := 0
	seen := map[string]bool{}
	for entry.Parent != "" && !seen[entry.Parent] {
		seen[entry.Parent] = true
		parent, ok := flat[entry.Parent]
		if !ok {
			break
		}
		depth++
		entry = parent
	}
	return depth
}

// applyPatches overlays the config patches and returns ownership information.
func applyPatches(entries []loader.EntryOptions, patches []Patch) ([]loader.EntryOptions, ownership, error) {
	entries = clone(entries)
	own := ownership{inserted: map[string]bool{}, overrides: map[string]map[string]bool{}}
	var insertCounter int
	for index, patch := range patches {
		if len(patch.Insert) > 0 {
			inserted := clone(patch.Insert)
			assignMissingIDs(inserted, "insert-", &insertCounter)
			if patch.ID == "" {
				entries = append(entries, inserted...)
			} else {
				ok := loader.UpdateInTree(entries, patch.ID, func(options *loader.EntryOptions) {
					options.Group = true
					options.Entries = append(options.Entries, inserted...)
				})
				if !ok {
					return nil, own, fmt.Errorf("patch %d: group %q not found", index, patch.ID)
				}
			}
			own.markInserted(inserted)
			continue
		}
		if patch.ID == "" {
			return nil, own, fmt.Errorf("patch %d: id is required", index)
		}
		ok := loader.UpdateInTree(entries, patch.ID, func(options *loader.EntryOptions) {
			if patch.Name != nil {
				options.Name = *patch.Name
				own.mark(patch.ID, "name")
			}
			if patch.Config != nil {
				options.Config = *patch.Config
				own.mark(patch.ID, "config")
			}
			if patch.Disabled != nil {
				options.Disabled = *patch.Disabled
				own.mark(patch.ID, "disabled")
			}
			if patch.Inject != nil {
				options.Inject = *patch.Inject
				own.mark(patch.ID, "inject")
			}
			if patch.Intercept != nil {
				options.Intercept = *patch.Intercept
				own.mark(patch.ID, "intercept")
			}
			if patch.Isolate != nil {
				options.Isolate = *patch.Isolate
				own.mark(patch.ID, "isolate")
			}
		})
		if !ok {
			return nil, own, fmt.Errorf("patch %d: entry %q not found", index, patch.ID)
		}
	}
	return entries, own, nil
}

func (i *Include) watch() error {
	watcher, err := fsnotify.NewWatcher()
	if err != nil {
		return err
	}
	if err := watcher.Add(filepath.Dir(i.path)); err != nil {
		_ = watcher.Close()
		return err
	}
	i.watcher = watcher

	if _, err := i.ctx.Effect("include.watch()", func() (cordis.Disposer, error) {
		go i.loop()
		return func() error { return watcher.Close() }, nil
	}); err != nil {
		_ = watcher.Close()
		return err
	}
	return nil
}

func (i *Include) loop() {
	var timer *time.Timer
	for {
		select {
		case event, ok := <-i.watcher.Events:
			if !ok {
				return
			}
			if filepath.Clean(event.Name) != filepath.Clean(i.path) {
				continue
			}
			if event.Op&(fsnotify.Write|fsnotify.Create|fsnotify.Rename) == 0 {
				continue
			}
			delay := i.config.Debounce
			if delay <= 0 {
				delay = 100 * time.Millisecond
			}
			if timer != nil {
				timer.Stop()
			}
			timer = time.AfterFunc(delay, func() {
				if err := i.Load(); err != nil {
					i.ctx.Logger("include").Warn(err)
				}
			})
		case err, ok := <-i.watcher.Errors:
			if !ok {
				return
			}
			i.ctx.Logger("include").Warn(err)
		}
	}
}

// assignIDs gives anonymous file entries stable ids across reads.
func (i *Include) assignIDs(entries []loader.EntryOptions) []loader.EntryOptions {
	entries = clone(entries)
	assignMissingIDs(entries, "anon-", &i.counter)
	return entries
}

func assignMissingIDs(entries []loader.EntryOptions, prefix string, counter *int) {
	used := map[string]bool{}
	collectIDs(entries, used)
	next := 0
	if counter != nil {
		next = *counter
	}
	var assign func(list []loader.EntryOptions)
	assign = func(list []loader.EntryOptions) {
		for index := range list {
			if list[index].ID == "" {
				for {
					next++
					id := fmt.Sprintf("%s%d", prefix, next)
					if !used[id] {
						list[index].ID = id
						used[id] = true
						break
					}
				}
			}
			assign(list[index].Entries)
		}
	}
	assign(entries)
	if counter != nil {
		*counter = next
	}
}

func collectIDs(entries []loader.EntryOptions, used map[string]bool) {
	for _, entry := range entries {
		if entry.ID != "" {
			used[entry.ID] = true
		}
		collectIDs(entry.Entries, used)
	}
}
