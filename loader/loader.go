package loader

import (
	"fmt"
	"reflect"
	"strings"
	"sync"
	"sync/atomic"

	"gocordis"
)

// Key is the loader service.
var Key = cordis.NewKey[*Loader]("loader")

// ChangeAction names a loader mutation.
type ChangeAction string

const (
	ChangeCreate  ChangeAction = "create"
	ChangeRemove  ChangeAction = "remove"
	ChangeUpdate  ChangeAction = "update"
	ChangeDisable ChangeAction = "disable"
)

// Change is reported after a runtime mutation of the entry tree. Applying a
// file does not report changes; only programmatic and component-driven
// mutations do, so a file source can write them back without echoing itself.
type Change struct {
	Action  ChangeAction
	ID      string
	Options EntryOptions
	Source  []EntryOptions
}

// Loader owns the entry tree and mounts its plugins.
type Loader struct {
	ctx      *cordis.Context
	resolver Resolver

	mu       sync.Mutex
	entries  map[string]*Entry
	root     map[string]*Entry
	source   []EntryOptions
	env      map[string]any
	counter  int
	onChange func(Change)
	byFiber  sync.Map
	applying atomic.Bool
	closing  atomic.Bool
}

// Entry is one mounted node of the tree.
type Entry struct {
	loader    *Loader
	options   EntryOptions
	ctx       *cordis.Context
	fiber     *cordis.Fiber
	children  map[string]*Entry
	parentMap map[string]*Entry

	suppressCommit  atomic.Bool
	suppressDispose atomic.Bool
}

// New creates a loader. The resolver decides what a name means.
func New(ctx *cordis.Context, resolver Resolver) (*Loader, error) {
	l := &Loader{
		ctx:      ctx,
		resolver: resolver,
		entries:  map[string]*Entry{},
		root:     map[string]*Entry{},
		env:      map[string]any{},
	}
	if _, err := ctx.Provide(Key, l); err != nil {
		return nil, err
	}
	if _, err := ctx.On("internal/commit", l.onCommit); err != nil {
		return nil, err
	}
	if _, err := ctx.On("internal/plugin", l.onPlugin); err != nil {
		return nil, err
	}
	return l, nil
}

// SetEnv adds a value visible to !!js expressions.
func (l *Loader) SetEnv(name string, value any) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.env[name] = value
}

// SetOnChange registers the receiver of runtime mutations.
func (l *Loader) SetOnChange(fn func(Change)) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.onChange = fn
}

// Source returns the current declarative tree.
func (l *Loader) Source() []EntryOptions {
	l.mu.Lock()
	defer l.mu.Unlock()
	return cloneEntries(l.source)
}

// Apply reconciles with a complete entry list. It does not report a Change.
func (l *Loader) Apply(options []EntryOptions) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.source = cloneEntries(options)
	l.ensureIDs(l.source)
	return l.applyLocked()
}

// Create inserts an entry under parent ("" for the root) and returns its id.
// Giving an existing id moves that entry.
func (l *Loader) Create(parent string, options EntryOptions) (string, error) {
	l.mu.Lock()
	options.ID = l.ensureID(options.ID, nil)
	if options.Name == "" && options.URL != "" {
		options.Name = options.URL
	}
	if !options.Group && options.Name == "" {
		l.mu.Unlock()
		return "", fmt.Errorf("entry %q requires a name", options.ID)
	}

	if found := l.entries[options.ID]; found != nil {
		l.removeEntry(found)
		if found.parentMap != nil {
			delete(found.parentMap, options.ID)
		}
	}
	l.source, _ = removeFromTree(l.source, options.ID)

	var inserted bool
	l.source, inserted = insertIntoTree(l.source, parent, options)
	if !inserted {
		l.mu.Unlock()
		return "", fmt.Errorf("group %q not found", parent)
	}
	err := l.applyLocked()
	l.mu.Unlock()
	if err != nil {
		return "", err
	}
	l.emit(ChangeCreate, options.ID, options)
	return options.ID, nil
}

// Remove deletes an entry and unmounts it.
func (l *Loader) Remove(id string) error {
	l.mu.Lock()
	_, found := removeFromTree(l.source, id)
	if !found {
		l.mu.Unlock()
		return fmt.Errorf("entry %q not found", id)
	}
	entry := l.entries[id]
	err := l.applyLocked()
	l.mu.Unlock()
	if err != nil {
		return err
	}
	options := EntryOptions{ID: id}
	if entry != nil {
		options = entry.options
	}
	l.emit(ChangeRemove, id, options)
	return nil
}

// Update replaces one entry and reconciles the tree.
func (l *Loader) Update(id string, options EntryOptions) error {
	l.mu.Lock()
	options.ID = id
	if options.Name == "" && options.URL != "" {
		options.Name = options.URL
	}
	updated := updateInTree(l.source, id, func(current *EntryOptions) {
		options.Entries = current.Entries
		if options.Group && len(options.Entries) == 0 {
			options.Entries = current.Entries
		}
		*current = options
	})
	if !updated {
		l.mu.Unlock()
		return fmt.Errorf("entry %q not found", id)
	}
	err := l.applyLocked()
	l.mu.Unlock()
	if err != nil {
		return err
	}
	l.emit(ChangeUpdate, id, options)
	return nil
}

// Entries returns a snapshot of mounted entries by id.
func (l *Loader) Entries() []EntryOptions {
	l.mu.Lock()
	defer l.mu.Unlock()
	result := make([]EntryOptions, 0, len(l.entries))
	for _, entry := range l.entries {
		result = append(result, entry.options)
	}
	return result
}

// Await waits for every mounted fiber to settle.
func (l *Loader) Await() {
	l.mu.Lock()
	fibers := make([]*cordis.Fiber, 0, len(l.entries))
	for _, entry := range l.entries {
		if entry.fiber != nil {
			fibers = append(fibers, entry.fiber)
		}
	}
	l.mu.Unlock()
	for _, fiber := range fibers {
		_ = fiber.Wait()
	}
}

// Close unmounts every entry.
func (l *Loader) Close() {
	l.closing.Store(true)
	l.mu.Lock()
	defer l.mu.Unlock()
	for id, entry := range l.root {
		l.removeEntry(entry)
		delete(l.root, id)
	}
	l.source = nil
}

func (l *Loader) applyLocked() error {
	l.applying.Store(true)
	defer l.applying.Store(false)
	return l.applyList(l.ctx, l.source, l.root)
}

func (l *Loader) ensureIDs(entries []EntryOptions) {
	for i := range entries {
		entries[i].ID = l.ensureID(entries[i].ID, nil)
		l.ensureIDs(entries[i].Entries)
	}
}

func (l *Loader) ensureID(id string, used func(string) bool) string {
	if id != "" {
		return id
	}
	for {
		l.counter++
		id = fmt.Sprintf("entry-%d", l.counter)
		if _, exists := l.entries[id]; exists {
			continue
		}
		if used != nil && used(id) {
			continue
		}
		return id
	}
}

func (l *Loader) applyList(parent *cordis.Context, options []EntryOptions, current map[string]*Entry) error {
	next := make(map[string]*Entry, len(options))
	for i := range options {
		opts := options[i]
		if existing := current[opts.ID]; existing != nil {
			entry, err := l.updateEntry(existing, opts, parent, current)
			if err != nil {
				return err
			}
			next[opts.ID] = entry
			continue
		}
		// The id may live in another group: rebuild it in its new place.
		if moved := l.entries[opts.ID]; moved != nil {
			l.removeEntry(moved)
			if moved.parentMap != nil {
				delete(moved.parentMap, opts.ID)
			}
		}
		entry, err := l.createEntry(parent, opts, current)
		if err != nil {
			return err
		}
		next[opts.ID] = entry
	}
	for id, entry := range current {
		if _, ok := next[id]; !ok {
			l.removeEntry(entry)
		}
	}
	for id := range current {
		delete(current, id)
	}
	for id, entry := range next {
		current[id] = entry
	}
	return nil
}

func (l *Loader) createEntry(parent *cordis.Context, options EntryOptions, parentMap map[string]*Entry) (*Entry, error) {
	if existing := l.entries[options.ID]; existing != nil {
		return nil, fmt.Errorf("duplicate entry id %q", options.ID)
	}
	if options.Name == "" && options.URL != "" {
		options.Name = options.URL
	}
	if !options.Group && options.Name == "" {
		return nil, fmt.Errorf("entry %q requires a name", options.ID)
	}
	if strings.Contains(options.ID, ":") {
		return nil, fmt.Errorf("entry id %q must not contain ':'", options.ID)
	}

	entry := &Entry{
		loader:    l,
		options:   options,
		children:  map[string]*Entry{},
		parentMap: parentMap,
	}
	entry.ctx = l.deriveContext(parent, entry)
	l.entries[options.ID] = entry

	if options.Group {
		if err := l.applyList(entry.ctx, options.Entries, entry.children); err != nil {
			l.removeEntry(entry)
			return nil, err
		}
		return entry, nil
	}

	disabled, err := l.isDisabled(entry.ctx, options.Disabled)
	if err != nil {
		l.removeEntry(entry)
		return nil, err
	}
	if disabled {
		return entry, nil
	}
	if err := l.mount(entry); err != nil {
		l.removeEntry(entry)
		return nil, err
	}
	return entry, nil
}

func (l *Loader) updateEntry(entry *Entry, options EntryOptions, parent *cordis.Context, parentMap map[string]*Entry) (*Entry, error) {
	if options.Name == "" && options.URL != "" {
		options.Name = options.URL
	}
	rebuild := entry.options.Name != options.Name ||
		entry.options.URL != options.URL ||
		entry.options.Group != options.Group ||
		!reflect.DeepEqual(entry.options.Isolate, options.Isolate) ||
		!reflect.DeepEqual(entry.options.Intercept, options.Intercept)
	if rebuild {
		l.removeEntry(entry)
		return l.createEntry(parent, options, parentMap)
	}

	previous := entry.options
	entry.options = options

	if options.Group {
		if err := l.applyList(entry.ctx, options.Entries, entry.children); err != nil {
			return nil, err
		}
		return entry, nil
	}

	disabled, err := l.isDisabled(entry.ctx, options.Disabled)
	if err != nil {
		return nil, err
	}
	if disabled {
		if entry.fiber != nil {
			entry.suppressDispose.Store(true)
			entry.fiber.Dispose()
			entry.suppressDispose.Store(false)
			l.byFiber.Delete(entry.fiber)
			entry.fiber = nil
		}
		return entry, nil
	}
	if entry.fiber == nil || entry.fiber.State() == cordis.StateDisposed {
		if err := l.mount(entry); err != nil {
			return nil, err
		}
		return entry, nil
	}
	if reflect.DeepEqual(previous.Config, options.Config) &&
		reflect.DeepEqual(previous.Inject, options.Inject) {
		return entry, nil
	}
	config, err := l.configFor(entry)
	if err != nil {
		return nil, err
	}
	entry.suppressCommit.Store(true)
	entry.fiber.Update(config)
	entry.suppressCommit.Store(false)
	return entry, nil
}

func (l *Loader) removeEntry(entry *Entry) {
	for _, child := range entry.children {
		l.removeEntry(child)
	}
	entry.children = map[string]*Entry{}
	if entry.fiber != nil {
		entry.suppressDispose.Store(true)
		entry.fiber.Dispose()
		entry.suppressDispose.Store(false)
		l.byFiber.Delete(entry.fiber)
		entry.fiber = nil
	}
	delete(l.entries, entry.options.ID)
}

func (l *Loader) deriveContext(parent *cordis.Context, entry *Entry) *cordis.Context {
	ctx := parent.Extend()
	for name, value := range entry.options.Isolate {
		label, ok := value.(string)
		if !ok {
			label = entry.options.ID
		}
		ctx = ctx.Isolate(name, label)
	}
	for name, value := range entry.options.Intercept {
		interpolated, err := l.interpolate(ctx, value)
		if err == nil {
			ctx = ctx.Intercept(name, interpolated)
		}
	}
	return ctx
}

func (l *Loader) mount(entry *Entry) error {
	plugin, err := l.resolver.Resolve(entry.options.Name)
	if err != nil {
		return err
	}
	if len(entry.options.Inject) > 0 {
		copy := *plugin
		copy.Inject = entry.options.Inject
		plugin = &copy
	}
	config, err := l.configFor(entry)
	if err != nil {
		return err
	}
	entry.suppressCommit.Store(true)
	entry.fiber = entry.ctx.Plugin(plugin, config)
	entry.suppressCommit.Store(false)
	l.byFiber.Store(entry.fiber, entry)
	return nil
}

func (l *Loader) configFor(entry *Entry) (any, error) {
	config, err := l.interpolate(entry.ctx, entry.options.Config)
	if err != nil {
		return nil, err
	}
	plugin, err := l.resolver.Resolve(entry.options.Name)
	if err == nil {
		if metadata, ok := entry.ctx.InterceptValue(plugin.Name); ok {
			config = mergeConfig(config, metadata)
		}
	}
	return config, nil
}

func mergeConfig(config, metadata any) any {
	if metadata == nil {
		return config
	}
	base, baseOK := config.(map[string]any)
	extra, extraOK := metadata.(map[string]any)
	if !baseOK || !extraOK {
		return metadata
	}
	merged := make(map[string]any, len(base)+len(extra))
	for key, value := range base {
		merged[key] = value
	}
	for key, value := range extra {
		merged[key] = value
	}
	return merged
}

func (l *Loader) isDisabled(ctx *cordis.Context, value any) (bool, error) {
	if value == nil {
		return false, nil
	}
	interpolated, err := l.interpolate(ctx, value)
	if err != nil {
		return true, err
	}
	disabled, ok := interpolated.(bool)
	if !ok {
		return true, fmt.Errorf("disabled must be a boolean, got %T", interpolated)
	}
	return disabled, nil
}

func (l *Loader) emit(action ChangeAction, id string, options EntryOptions) {
	l.mu.Lock()
	fn := l.onChange
	source := cloneEntries(l.source)
	l.mu.Unlock()
	if fn != nil {
		fn(Change{Action: action, ID: id, Options: options, Source: source})
	}
}

func (l *Loader) onCommit(_ *cordis.Context, args ...any) any {
	if len(args) < 2 {
		return nil
	}
	fiber, ok := args[0].(*cordis.Fiber)
	if !ok {
		return nil
	}
	value, ok := l.byFiber.Load(fiber)
	if !ok {
		return nil
	}
	entry := value.(*Entry)
	if entry.suppressCommit.Load() || l.applying.Load() {
		return nil
	}
	config := args[1]

	l.mu.Lock()
	entry.options.Config = config
	updateInTree(l.source, entry.options.ID, func(options *EntryOptions) { options.Config = config })
	l.mu.Unlock()
	l.emit(ChangeUpdate, entry.options.ID, entry.options)
	return nil
}

func (l *Loader) onPlugin(_ *cordis.Context, args ...any) any {
	if len(args) == 0 {
		return nil
	}
	fiber, ok := args[0].(*cordis.Fiber)
	if !ok || fiber.UID() > 0 {
		return nil
	}
	value, ok := l.byFiber.Load(fiber)
	if !ok {
		return nil
	}
	entry := value.(*Entry)
	if entry.suppressDispose.Load() || l.applying.Load() {
		return nil
	}
	if l.closing.Load() || l.ctx.Closing() {
		return nil
	}

	l.mu.Lock()
	if entry.options.Disabled != nil {
		if disabled, _ := entry.options.Disabled.(bool); disabled {
			l.mu.Unlock()
			return nil
		}
	}
	entry.options.Disabled = true
	updateInTree(l.source, entry.options.ID, func(options *EntryOptions) { options.Disabled = true })
	l.mu.Unlock()
	l.emit(ChangeDisable, entry.options.ID, entry.options)
	return nil
}

func cloneEntries(entries []EntryOptions) []EntryOptions {
	if entries == nil {
		return nil
	}
	result := make([]EntryOptions, len(entries))
	for i, entry := range entries {
		result[i] = entry
		result[i].Entries = cloneEntries(entry.Entries)
	}
	return result
}

func insertIntoTree(list []EntryOptions, parent string, options EntryOptions) ([]EntryOptions, bool) {
	if parent == "" {
		return append(list, options), true
	}
	for i := range list {
		if list[i].ID == parent && list[i].Group {
			list[i].Entries = append(list[i].Entries, options)
			return list, true
		}
		if list[i].Group {
			if next, ok := insertIntoTree(list[i].Entries, parent, options); ok {
				list[i].Entries = next
				return list, true
			}
		}
	}
	return list, false
}

func removeFromTree(list []EntryOptions, id string) ([]EntryOptions, bool) {
	for i := range list {
		if list[i].ID == id {
			return append(list[:i], list[i+1:]...), true
		}
		if list[i].Group {
			if next, ok := removeFromTree(list[i].Entries, id); ok {
				list[i].Entries = next
				return list, true
			}
		}
	}
	return list, false
}

func updateInTree(list []EntryOptions, id string, fn func(*EntryOptions)) bool {
	for i := range list {
		if list[i].ID == id {
			fn(&list[i])
			return true
		}
		if list[i].Group && updateInTree(list[i].Entries, id, fn) {
			return true
		}
	}
	return false
}
