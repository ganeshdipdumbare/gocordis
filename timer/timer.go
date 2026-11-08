// Package timer provides effect-backed timers for a cordis context.
package timer

import (
	"sync"
	"time"

	"gocordis"
)

// Key is the service used to look up the timer.
var Key = cordis.NewKey[*Service]("timer")

// Service schedules callbacks and cancels them with the owning fiber.
type Service struct {
	ctx *cordis.Context
}

// New registers the timer on ctx.
func New(ctx *cordis.Context) (*Service, error) {
	service := &Service{ctx: ctx}
	if _, err := ctx.Provide(Key, service); err != nil {
		return nil, err
	}
	return service, nil
}

// Timeout calls fn once after delay. Disposing cancels the call.
func (s *Service) Timeout(delay time.Duration, fn func()) (cordis.Disposer, error) {
	return s.ctx.Effect("timer.timeout()", func() (cordis.Disposer, error) {
		timer := time.AfterFunc(delay, fn)
		return func() error {
			timer.Stop()
			return nil
		}, nil
	})
}

// Interval calls fn every delay until disposed.
func (s *Service) Interval(delay time.Duration, fn func()) (cordis.Disposer, error) {
	return s.ctx.Effect("timer.interval()", func() (cordis.Disposer, error) {
		ticker := time.NewTicker(delay)
		done := make(chan struct{})
		finished := make(chan struct{})
		go func() {
			defer close(finished)
			for {
				select {
				case <-done:
					return
				case <-ticker.C:
					fn()
				}
			}
		}()
		var once sync.Once
		return func() error {
			once.Do(func() {
				close(done)
				ticker.Stop()
				<-finished
			})
			return nil
		}, nil
	})
}

// Debounce returns a trigger that runs fn once the delay elapses without
// another call. Disposing the returned disposer cancels a pending run.
func (s *Service) Debounce(delay time.Duration, fn func()) (func(), cordis.Disposer, error) {
	var mu sync.Mutex
	var timer *time.Timer
	dispose, err := s.ctx.Effect("timer.debounce()", func() (cordis.Disposer, error) {
		return func() error {
			mu.Lock()
			if timer != nil {
				timer.Stop()
				timer = nil
			}
			mu.Unlock()
			return nil
		}, nil
	})
	if err != nil {
		return nil, nil, err
	}
	trigger := func() {
		mu.Lock()
		defer mu.Unlock()
		if timer != nil {
			timer.Stop()
		}
		timer = time.AfterFunc(delay, fn)
	}
	return trigger, dispose, nil
}

// Throttle returns a trigger that runs fn at most once per delay.
func (s *Service) Throttle(delay time.Duration, fn func()) (func(), cordis.Disposer, error) {
	var mu sync.Mutex
	var last time.Time
	dispose, err := s.ctx.Effect("timer.throttle()", func() (cordis.Disposer, error) {
		return func() error { return nil }, nil
	})
	if err != nil {
		return nil, nil, err
	}
	trigger := func() {
		mu.Lock()
		defer mu.Unlock()
		if !last.IsZero() && time.Since(last) < delay {
			return
		}
		last = time.Now()
		fn()
	}
	return trigger, dispose, nil
}
