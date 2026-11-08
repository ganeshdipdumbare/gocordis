package loader

import (
	"fmt"
	"testing"

	"gopkg.in/yaml.v3"

	"gocordis"
)

func newTestLoader(t *testing.T, applied *[]any) *Loader {
	t.Helper()
	root := cordis.New()
	catalog := NewCatalog()
	catalog.Add("hello", &cordis.Plugin{
		Name: "hello",
		Apply: func(_ *cordis.Context, config any) error {
			*applied = append(*applied, config)
			return nil
		},
	})
	l, err := New(root, catalog)
	if err != nil {
		t.Fatal(err)
	}
	return l
}

func TestApplyEntryTree(t *testing.T) {
	var applied []any
	l := newTestLoader(t, &applied)

	var options []EntryOptions
	err := yaml.Unmarshal([]byte(`
- id: a
  name: hello
  config:
    value: 1
- id: b
  name: hello
  disabled: true
  config: ignored
- id: group
  group: true
  config:
    - id: c
      name: hello
      config:
        value: 2
`), &options)
	if err != nil {
		t.Fatal(err)
	}
	if err := l.Apply(options); err != nil {
		t.Fatal(err)
	}
	l.Await()

	if len(applied) != 2 {
		t.Fatalf("applied %d plugins, want 2: %v", len(applied), applied)
	}
	if got := fmt.Sprint(applied[0]); got != "map[value:1]" {
		t.Fatalf("first config = %s", got)
	}
	if got := fmt.Sprint(applied[1]); got != "map[value:2]" {
		t.Fatalf("second config = %s", got)
	}
}

func TestDisabledExpression(t *testing.T) {
	var applied []any
	l := newTestLoader(t, &applied)

	var options []EntryOptions
	err := yaml.Unmarshal([]byte(`
- id: yes
  name: hello
  disabled: !!js "1 > 2"
- id: no
  name: hello
  disabled: !!js "2 > 1"
`), &options)
	if err != nil {
		t.Fatal(err)
	}
	if err := l.Apply(options); err != nil {
		t.Fatal(err)
	}
	l.Await()
	if len(applied) != 1 {
		t.Fatalf("applied %d plugins, want 1", len(applied))
	}
}

func TestUpdateEntryConfig(t *testing.T) {
	var applied []any
	l := newTestLoader(t, &applied)

	first := []EntryOptions{{ID: "a", Name: "hello", Config: map[string]any{"value": 1}}}
	if err := l.Apply(first); err != nil {
		t.Fatal(err)
	}
	second := []EntryOptions{{ID: "a", Name: "hello", Config: map[string]any{"value": 2}}}
	if err := l.Apply(second); err != nil {
		t.Fatal(err)
	}
	l.Await()

	if len(applied) != 2 {
		t.Fatalf("applied %d plugins, want 2: %v", len(applied), applied)
	}
	if got := fmt.Sprint(applied[1]); got != "map[value:2]" {
		t.Fatalf("reloaded config = %s", got)
	}
}

func TestMissingPluginFails(t *testing.T) {
	var applied []any
	l := newTestLoader(t, &applied)
	err := l.Apply([]EntryOptions{{ID: "x", Name: "missing"}})
	if err == nil {
		t.Fatal("expected an error for an unregistered plugin")
	}
}
