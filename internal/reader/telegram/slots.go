package telegram

import (
	"context"
	"sync"
	"time"
)

// fetchSlots bounds concurrent channel fetches so a burst of due feeds does
// not hit the proxy service all at once. Waiters block until a slot frees or
// ctx ends; the limit can change at runtime.
type fetchSlots struct {
	mu       sync.Mutex
	cond     *sync.Cond
	limit    int
	inFlight int
}

func newFetchSlots(limit int) *fetchSlots {
	s := &fetchSlots{limit: ClampConcurrentSlots(limit)}
	s.cond = sync.NewCond(&s.mu)
	return s
}

func (s *fetchSlots) setLimit(limit int) {
	s.mu.Lock()
	s.limit = ClampConcurrentSlots(limit)
	s.cond.Broadcast()
	s.mu.Unlock()
}

func (s *fetchSlots) acquire(ctx context.Context) error {
	stop := context.AfterFunc(ctx, func() {
		s.mu.Lock()
		s.cond.Broadcast()
		s.mu.Unlock()
	})
	defer stop()

	s.mu.Lock()
	defer s.mu.Unlock()
	for s.inFlight >= s.limit {
		if err := ctx.Err(); err != nil {
			return err
		}
		s.cond.Wait()
	}
	s.inFlight++
	return nil
}

func (s *fetchSlots) release() {
	s.mu.Lock()
	s.inFlight--
	s.cond.Signal()
	s.mu.Unlock()
}

func ClampConcurrentSlots(n int) int {
	return min(max(n, 1), 32)
}

// ErrSlotsBusy is returned when no fetch slot freed up before the deadline.
// It is a RetryAt error: the poll is deferred, not counted as a failure.
// The text carries no timestamp so repeated occurrences coalesce in the poll log.
type ErrSlotsBusy struct {
	Until time.Time
}

func (e *ErrSlotsBusy) Error() string {
	return "telegram: no free fetch slot within the fetch timeout"
}

func (e *ErrSlotsBusy) RetryAt() time.Time { return e.Until }
