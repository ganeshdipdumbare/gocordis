package timer

import (
	"sync/atomic"
	"testing"
	"time"

	"gocordis"
)

func TestTimeout(t *testing.T) {
	root := cordis.New()
	service, err := New(root)
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan struct{}, 1)
	if _, err := service.Timeout(10*time.Millisecond, func() { done <- struct{}{} }); err != nil {
		t.Fatal(err)
	}
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("timeout did not fire")
	}
}

func TestTimeoutDisposeCancels(t *testing.T) {
	root := cordis.New()
	service, err := New(root)
	if err != nil {
		t.Fatal(err)
	}
	var calls atomic.Int32
	dispose, err := service.Timeout(30*time.Millisecond, func() { calls.Add(1) })
	if err != nil {
		t.Fatal(err)
	}
	if err := dispose(); err != nil {
		t.Fatal(err)
	}
	time.Sleep(60 * time.Millisecond)
	if calls.Load() != 0 {
		t.Fatalf("callback ran %d times after dispose", calls.Load())
	}
}

func TestIntervalStopsOnDispose(t *testing.T) {
	root := cordis.New()
	service, err := New(root)
	if err != nil {
		t.Fatal(err)
	}
	var calls atomic.Int32
	dispose, err := service.Interval(5*time.Millisecond, func() { calls.Add(1) })
	if err != nil {
		t.Fatal(err)
	}
	time.Sleep(30 * time.Millisecond)
	if err := dispose(); err != nil {
		t.Fatal(err)
	}
	after := calls.Load()
	time.Sleep(30 * time.Millisecond)
	if calls.Load() != after {
		t.Fatalf("interval kept firing after dispose: %d -> %d", after, calls.Load())
	}
}

func TestDebounce(t *testing.T) {
	root := cordis.New()
	service, err := New(root)
	if err != nil {
		t.Fatal(err)
	}
	var calls atomic.Int32
	trigger, _, err := service.Debounce(20*time.Millisecond, func() { calls.Add(1) })
	if err != nil {
		t.Fatal(err)
	}
	trigger()
	trigger()
	trigger()
	time.Sleep(80 * time.Millisecond)
	if calls.Load() != 1 {
		t.Fatalf("debounced %d calls, want 1", calls.Load())
	}
}
