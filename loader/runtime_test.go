package loader

import (
	"testing"

	"gocordis"
)

func TestRuntimeMutationsReportChanges(t *testing.T) {
	root := cordis.New()
	catalog := NewCatalog()
	catalog.Add("hello", &cordis.Plugin{Name: "hello", Apply: func(*cordis.Context, any) error { return nil }})
	l, err := New(root, catalog)
	if err != nil {
		t.Fatal(err)
	}

	var changes []Change
	l.SetOnChange(func(change Change) { changes = append(changes, change) })

	if _, err := l.Create("", EntryOptions{ID: "a", Name: "hello"}); err != nil {
		t.Fatal(err)
	}
	if err := l.Update("a", EntryOptions{Name: "hello", Config: map[string]any{"value": 1}}); err != nil {
		t.Fatal(err)
	}
	if err := l.Remove("a"); err != nil {
		t.Fatal(err)
	}

	if len(changes) != 3 {
		t.Fatalf("changes = %d, want 3", len(changes))
	}
	if changes[0].Action != ChangeCreate || changes[1].Action != ChangeUpdate || changes[2].Action != ChangeRemove {
		t.Fatalf("actions = %v", changes)
	}
}

func TestMoveEntryBetweenGroups(t *testing.T) {
	root := cordis.New()
	catalog := NewCatalog()
	catalog.Add("hello", &cordis.Plugin{Name: "hello", Apply: func(*cordis.Context, any) error { return nil }})
	l, err := New(root, catalog)
	if err != nil {
		t.Fatal(err)
	}

	err = l.Apply([]EntryOptions{
		{ID: "left", Group: true, Entries: []EntryOptions{{ID: "child", Name: "hello"}}},
		{ID: "right", Group: true},
	})
	if err != nil {
		t.Fatal(err)
	}

	if _, err := l.Create("right", EntryOptions{ID: "child", Name: "hello"}); err != nil {
		t.Fatal(err)
	}
	source := l.Source()
	right := source[1]
	if right.ID != "right" || len(right.Entries) != 1 || right.Entries[0].ID != "child" {
		t.Fatalf("move failed: %+v", source)
	}
	if len(source[0].Entries) != 0 {
		t.Fatalf("child remained in left group: %+v", source[0])
	}
}

func TestInterceptMergesIntoConfig(t *testing.T) {
	root := cordis.New()
	catalog := NewCatalog()
	var seen any
	catalog.Add("hello", &cordis.Plugin{Name: "hello", Apply: func(_ *cordis.Context, config any) error {
		seen = config
		return nil
	}})
	l, err := New(root, catalog)
	if err != nil {
		t.Fatal(err)
	}
	err = l.Apply([]EntryOptions{{
		ID:        "a",
		Name:      "hello",
		Config:    map[string]any{"message": "file"},
		Intercept: map[string]any{"hello": map[string]any{"extra": "intercept"}},
	}})
	if err != nil {
		t.Fatal(err)
	}
	mapping, ok := seen.(map[string]any)
	if !ok {
		t.Fatalf("config = %#v", seen)
	}
	if mapping["message"] != "file" || mapping["extra"] != "intercept" {
		t.Fatalf("merged config = %v", mapping)
	}
}

func TestURLIsAnAliasForName(t *testing.T) {
	root := cordis.New()
	catalog := NewCatalog()
	catalog.Add("hello", &cordis.Plugin{Name: "hello", Apply: func(*cordis.Context, any) error { return nil }})
	l, err := New(root, catalog)
	if err != nil {
		t.Fatal(err)
	}
	if err := l.Apply([]EntryOptions{{ID: "a", URL: "hello"}}); err != nil {
		t.Fatal(err)
	}
	if len(l.Entries()) != 1 {
		t.Fatalf("entries = %v", l.Entries())
	}
}

func TestEntryValidation(t *testing.T) {
	root := cordis.New()
	catalog := NewCatalog()
	l, err := New(root, catalog)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := l.Create("", EntryOptions{ID: "a"}); err == nil {
		t.Fatal("entry without a name should fail")
	}
	if _, err := l.Create("missing", EntryOptions{ID: "b", Name: "hello"}); err == nil {
		t.Fatal("missing group should fail")
	}
	if err := l.Apply([]EntryOptions{{ID: "bad:id", Name: "hello"}}); err == nil {
		t.Fatal("id with ':' should fail")
	}
}

func TestMissingPluginStillFails(t *testing.T) {
	var applied []any
	l := newTestLoader(t, &applied)
	err := l.Apply([]EntryOptions{{ID: "x", Name: "missing"}})
	if err == nil {
		t.Fatal("expected an error for an unregistered plugin")
	}
}
