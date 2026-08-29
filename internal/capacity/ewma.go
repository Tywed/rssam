package capacity

import (
	"sync"
	"time"
)

const ewmaAlpha = 0.2

var (
	pollMu      sync.Mutex
	pollEWMA    float64
	pollSamples int
)

// ObservePollDuration records a completed poll for the in-process EWMA.
func ObservePollDuration(d time.Duration) {
	if d < 0 {
		return
	}
	sec := d.Seconds()
	pollMu.Lock()
	defer pollMu.Unlock()
	pollSamples++
	if pollSamples == 1 {
		pollEWMA = sec
		return
	}
	pollEWMA = ewmaAlpha*sec + (1-ewmaAlpha)*pollEWMA
}

// PollDurationSnapshot returns the EWMA seconds and number of samples.
func PollDurationSnapshot() (avg time.Duration, samples int) {
	pollMu.Lock()
	defer pollMu.Unlock()
	if pollSamples == 0 {
		return 0, 0
	}
	return time.Duration(pollEWMA * float64(time.Second)), pollSamples
}

// ResetPollDurationForTest clears EWMA state. Tests only.
func ResetPollDurationForTest() {
	pollMu.Lock()
	defer pollMu.Unlock()
	pollEWMA = 0
	pollSamples = 0
}
