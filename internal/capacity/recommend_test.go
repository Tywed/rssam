package capacity

import (
	"testing"
	"time"
)

func TestRecommend_OK_FewFeeds(t *testing.T) {
	got := Recommend(Input{
		Workers:       10,
		OfferedPerMin: 180, // ~10 workers at 2s * 1.3 headroom
		AvgDuration:   2 * time.Second,
		Samples:       20,
		Overdue:       0,
		MaxLag:        0,
		Waiting:       0,
		ErrorFraction: 0,
	})
	if got.Action != ActionOK {
		t.Fatalf("action=%s want ok; %+v", got.Action, got)
	}
}

func TestRecommend_Increase_ShortIntervals(t *testing.T) {
	got := Recommend(Input{
		Workers:       10,
		OfferedPerMin: 400, // many 1-minute feeds
		AvgDuration:   2 * time.Second,
		Samples:       50,
		Overdue:       0,
		ErrorFraction: 0,
	})
	if got.Action != ActionIncrease {
		t.Fatalf("action=%s want increase; %+v", got.Action, got)
	}
	if got.SuggestedWorkers <= 10 {
		t.Fatalf("suggested=%d want > 10", got.SuggestedWorkers)
	}
	if got.SuggestedWorkers > 20 {
		t.Fatalf("step capped at ×2, suggested=%d", got.SuggestedWorkers)
	}
}

func TestRecommend_Increase_LagWithoutErrors(t *testing.T) {
	got := Recommend(Input{
		Workers:       10,
		OfferedPerMin: 5,
		AvgDuration:   time.Second,
		Samples:       10,
		Overdue:       25,
		MaxLag:        2 * time.Minute,
		ErrorFraction: 0.05,
	})
	if got.Action != ActionIncrease {
		t.Fatalf("action=%s want increase; %+v", got.Action, got)
	}
}

func TestRecommend_FixFeeds_LagWithErrors(t *testing.T) {
	got := Recommend(Input{
		Workers:       10,
		OfferedPerMin: 20,
		AvgDuration:   2 * time.Second,
		Samples:       10,
		Overdue:       30,
		MaxLag:        2 * time.Minute,
		ErrorFraction: 0.4,
	})
	if got.Action != ActionFixFeeds {
		t.Fatalf("action=%s want fix_feeds; %+v", got.Action, got)
	}
	if got.SuggestedWorkers != 10 {
		t.Fatalf("should not bump workers on fix_feeds, got %d", got.SuggestedWorkers)
	}
}

func TestRecommend_SchedulerLimit(t *testing.T) {
	got := Recommend(Input{
		Workers:       10,
		OfferedPerMin: 5,
		AvgDuration:   time.Second,
		Samples:       10,
		Overdue:       2,
		MaxLag:        time.Second,
		Waiting:       1500,
		ErrorFraction: 0,
	})
	if got.Action != ActionSchedulerLimit {
		t.Fatalf("action=%s want scheduler_limit; %+v", got.Action, got)
	}
}

func TestRecommend_Decrease(t *testing.T) {
	got := Recommend(Input{
		Workers:       20,
		OfferedPerMin: 1,
		AvgDuration:   time.Second,
		Samples:       30,
		Overdue:       0,
		MaxLag:        time.Second,
		ErrorFraction: 0,
	})
	if got.Action != ActionDecrease {
		t.Fatalf("action=%s want decrease; %+v", got.Action, got)
	}
	if got.SuggestedWorkers < 2 {
		t.Fatalf("suggested=%d want >= 2", got.SuggestedWorkers)
	}
	if got.SuggestedWorkers >= 20 {
		t.Fatalf("suggested=%d want < 20", got.SuggestedWorkers)
	}
}

func TestRecommend_FallbackTimeout(t *testing.T) {
	got := Recommend(Input{
		Workers:       2,
		OfferedPerMin: 60,
		Samples:       0,
		FetchTimeout:  15 * time.Second,
		ErrorFraction: 0,
	})
	if got.Action != ActionIncrease {
		t.Fatalf("action=%s want increase with timeout fallback; %+v", got.Action, got)
	}
}

func TestObservePollDuration_EWMA(t *testing.T) {
	ResetPollDurationForTest()
	ObservePollDuration(10 * time.Second)
	ObservePollDuration(time.Second)
	avg, n := PollDurationSnapshot()
	if n != 2 {
		t.Fatalf("samples=%d", n)
	}
	if avg >= 10*time.Second || avg <= time.Second {
		t.Fatalf("ewma=%s want between 1s and 10s", avg)
	}
}
