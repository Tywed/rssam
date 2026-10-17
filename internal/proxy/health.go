package proxy

import (
	"errors"
	"fmt"
	"sync"
	"time"
)

// Unavailable is a fetch that never reached the source because the proxy
// path failed: no proxy from the service, the SOCKS connection, or a
// status the proxy's IP earned. The feed is not at fault, so the refresher
// defers the poll to its normal schedule instead of counting an error.
type Unavailable struct {
	Op  string
	Err error
}

func (e *Unavailable) Error() string { return fmt.Sprintf("proxy: %s: %v", e.Op, e.Err) }
func (e *Unavailable) Unwrap() error { return e.Err }

// IsUnavailable reports whether err is (or wraps) an Unavailable.
func IsUnavailable(err error) bool {
	var u *Unavailable
	return errors.As(err, &u)
}

// Outage thresholds: an alert needs this many consecutive proxy failures
// spanning at least this long, so a single bad proxy swap stays quiet.
const (
	outageMinFailures = 20
	outageMinDuration = 10 * time.Minute
)

// Health counts consecutive proxy-path failures across all feeds of a
// Client; one success resets it.
type Health struct {
	mu       sync.Mutex
	failures int
	since    time.Time
	lastErr  string
}

func (h *Health) Failure(err error) {
	if h == nil {
		return
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.failures == 0 {
		h.since = time.Now()
	}
	h.failures++
	if err != nil {
		h.lastErr = truncate(err.Error(), 200)
	}
}

func (h *Health) Success() {
	if h == nil {
		return
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	h.failures = 0
	h.since = time.Time{}
	h.lastErr = ""
}

// OutageStatus is the current failure streak when it qualifies as an
// outage; ok=false otherwise.
type OutageStatus struct {
	Since    time.Time
	Failures int
	LastErr  string
}

func (h *Health) Outage() (OutageStatus, bool) {
	if h == nil {
		return OutageStatus{}, false
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.failures < outageMinFailures || time.Since(h.since) < outageMinDuration {
		return OutageStatus{}, false
	}
	return OutageStatus{Since: h.since, Failures: h.failures, LastErr: h.lastErr}, true
}
