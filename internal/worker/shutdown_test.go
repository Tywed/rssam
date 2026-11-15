package worker

import (
	"testing"
	"time"
)

func TestDrainTimeoutFor(t *testing.T) {
	cases := map[time.Duration]time.Duration{
		0:                defaultDrainTimeout,
		15 * time.Second: 12 * time.Second,
		2 * time.Second:  time.Second,
		time.Minute:      57 * time.Second,
	}
	for in, want := range cases {
		if got := DrainTimeoutFor(in); got != want {
			t.Errorf("DrainTimeoutFor(%v)=%v want %v", in, got, want)
		}
	}
}
