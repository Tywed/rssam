package capacity

import (
	"fmt"
	"math"
	"time"
)

const (
	ActionOK             = "ok"
	ActionIncrease       = "increase"
	ActionDecrease       = "decrease"
	ActionFixFeeds       = "fix_feeds"
	ActionSchedulerLimit = "scheduler_limit"

	SchedulerDueLimit = 1000
	maxWorkers        = 1000
	headroom          = 1.3
	errorFractionHigh = 0.25
	lagHigh           = 60 * time.Second
	lagIdle           = 5 * time.Second
	decreaseRatio     = 0.4
	minWorkersKeep    = 2
)

// Input is a snapshot used to recommend poll-worker pool size.
type Input struct {
	Workers       int
	OfferedPerMin float64
	AvgDuration   time.Duration
	Samples       int
	FetchTimeout  time.Duration
	Overdue       int
	MaxLag        time.Duration
	Waiting       int
	ErrorFraction float64
}

// Advice is a poll-pool recommendation (no auto-apply).
type Advice struct {
	Action           string
	SuggestedWorkers int
	NeededWorkers    int
	AvgDuration      time.Duration
	Utilization      float64
	Message          string
}

func avgPollSeconds(in Input) float64 {
	if in.Samples > 0 && in.AvgDuration > 0 {
		return in.AvgDuration.Seconds()
	}
	if in.FetchTimeout > 0 {
		return in.FetchTimeout.Seconds()
	}
	return 15
}

func neededWorkers(offeredPerMin, avgSec float64) int {
	if offeredPerMin <= 0 {
		return 1
	}
	n := math.Ceil(offeredPerMin * avgSec / 60 * headroom)
	if n < 1 {
		n = 1
	}
	if n > maxWorkers {
		n = maxWorkers
	}
	return int(n)
}

func capIncreaseStep(current, needed int) int {
	if current < 1 {
		current = 1
	}
	maxUp := current * 2
	if needed > maxUp {
		return maxUp
	}
	return needed
}

func utilization(offeredPerMin, avgSec float64, workers int) float64 {
	if workers <= 0 || avgSec <= 0 {
		return 0
	}
	capacity := float64(workers) * (60 / avgSec)
	if capacity <= 0 {
		return 0
	}
	u := offeredPerMin / capacity
	if u < 0 {
		return 0
	}
	return u
}

// Recommend returns a poll-worker action. First matching rule wins.
func Recommend(in Input) Advice {
	workers := max(in.Workers, 1)
	avgSec := avgPollSeconds(in)
	avgDur := time.Duration(avgSec * float64(time.Second))
	needed := neededWorkers(in.OfferedPerMin, avgSec)
	util := utilization(in.OfferedPerMin, avgSec, workers)

	base := Advice{
		SuggestedWorkers: workers,
		NeededWorkers:    needed,
		AvgDuration:      avgDur,
		Utilization:      util,
	}

	if in.ErrorFraction >= errorFractionHigh && (in.MaxLag >= lagHigh || in.Overdue > workers) {
		base.Action = ActionFixFeeds
		base.Message = "Много лент с ошибками и очередь опроса отстаёт. Сначала почините, поставьте на паузу или увеличьте интервалы; больше воркеров скорее усилит stampede и таймауты."
		return base
	}

	if in.Waiting >= SchedulerDueLimit && in.Overdue*2 < in.Waiting {
		base.Action = ActionSchedulerLimit
		base.Message = fmt.Sprintf("Ждут обновления %d лент при небольшой просроченной очереди jobs. Узкое место — лимит планировщика (до %d лент за тик), а не размер пула воркеров.", in.Waiting, SchedulerDueLimit)
		return base
	}

	lagBurst := in.MaxLag >= lagHigh && in.Overdue > 2*workers
	if needed > workers || lagBurst {
		suggested := capIncreaseStep(workers, needed)
		if lagBurst && suggested <= workers {
			suggested = capIncreaseStep(workers, workers+1)
		}
		base.Action = ActionIncrease
		base.SuggestedWorkers = suggested
		base.Message = fmt.Sprintf("Нагрузка опроса выше ёмкости пула (нужно около %d воркеров). Увеличьте WORKER_POOL_SIZE до %d и перезапустите сервис.", needed, suggested)
		return base
	}

	if workers > minWorkersKeep && float64(needed) < decreaseRatio*float64(workers) && in.MaxLag < lagIdle && in.Overdue <= workers {
		suggested := max(needed, minWorkersKeep)
		if suggested > needed && needed >= minWorkersKeep {
			suggested = needed
		}
		if suggested < minWorkersKeep {
			suggested = minWorkersKeep
		}
		if suggested >= workers {
			base.Action = ActionOK
			base.Message = "Пул опроса справляется с текущим числом лент и интервалами."
			return base
		}
		base.Action = ActionDecrease
		base.SuggestedWorkers = suggested
		base.Message = fmt.Sprintf("Пул заметно больше расчётной потребности (~%d). Можно снизить WORKER_POOL_SIZE до %d и перезапустить сервис.", needed, suggested)
		return base
	}

	base.Action = ActionOK
	base.Message = "Пул опроса справляется с текущим числом лент и интервалами."
	return base
}
