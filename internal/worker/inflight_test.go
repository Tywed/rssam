package worker

import (
	"slices"
	"testing"
	"time"
)

func TestInflightPolls(t *testing.T) {
	var p inflightPolls
	p.init(2 * time.Minute)
	t0 := time.Now()

	p.start(1, t0)
	p.start(2, t0.Add(-3*time.Minute))
	if ex := p.exclude(t0); !slices.Equal(ex, []int64{1}) {
		// Feed 2 ran past staleAfter: reported, no longer excluded.
		t.Fatalf("exclude=%v, want [1]", ex)
	}
	if running, stale := p.counts(t0); running != 2 || stale != 1 {
		t.Fatalf("running=%d stale=%d", running, stale)
	}
	if since, ok := p.since(1); !ok || !since.Equal(t0) {
		t.Fatalf("since(1)=%v ok=%v", since, ok)
	}

	// A finished poll stays excluded for the grace period only.
	p.done(1, t0.Add(time.Second))
	if _, ok := p.since(1); ok {
		t.Fatal("finished poll still reported running")
	}
	if ex := p.exclude(t0.Add(1500 * time.Millisecond)); !slices.Equal(ex, []int64{1}) {
		t.Fatalf("exclude inside grace=%v, want [1]", ex)
	}
	if ex := p.exclude(t0.Add(3 * time.Second)); len(ex) != 0 {
		t.Fatalf("exclude after grace=%v, want none", ex)
	}
	if running, _ := p.counts(t0.Add(3 * time.Second)); running != 1 {
		t.Fatalf("running=%d, want the stale one", running)
	}
	if _, ok := p.since(3); ok {
		t.Fatal("unknown feed reported running")
	}
}
