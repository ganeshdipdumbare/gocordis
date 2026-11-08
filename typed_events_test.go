package cordis

import "testing"

func TestTypedEvent(t *testing.T) {
	root := New()
	type tick struct{ count int }
	event := NewEvent[tick]("typed.tick")

	var seen int
	if _, err := event.On(root, func(_ *Context, value tick) { seen = value.count }); err != nil {
		t.Fatal(err)
	}
	event.Emit(root, tick{count: 3})
	if seen != 3 {
		t.Fatalf("seen = %d, want 3", seen)
	}
}

func TestTypedWaterfall(t *testing.T) {
	root := New()
	waterfall := NewWaterfall[string]("typed.wf")
	if _, err := waterfall.On(root, func(_ *Context, value string, next func() string) string {
		return "(" + next() + ")"
	}); err != nil {
		t.Fatal(err)
	}
	got := waterfall.Run(root, "x", func() string { return "x" })
	if got != "(x)" {
		t.Fatalf("got = %q, want (x)", got)
	}
}
