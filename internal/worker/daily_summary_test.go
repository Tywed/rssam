package worker

import (
	"strings"
	"testing"
	"time"

	"rssam/internal/storage"
)

func TestDailySummaryMessage(t *testing.T) {
	until := time.Date(2026, 11, 22, 21, 0, 0, 0, time.UTC)
	s := storage.DailySummary{
		Since: until.Add(-24 * time.Hour), Until: until,
		ByType: []storage.DailySummaryTypeRow{
			{FeedType: "max", Feeds: 10, ErrorPolls: 0, Inserted: 0},
			{FeedType: "telegram", Feeds: 40, ErrorPolls: 93, Inserted: 1234, ErrorFeeds: 26},
		},
		WebhookSent: 1200, WebhookFailed: 3, WebhookDead: 1, WebhookPending: 2,
		Feeds:     storage.AdminFeedSummary{TotalFeeds: 2251, OKCount: 2200, ErrorCount: 36, GoneCount: 30, BlockedCount: 6, PausedCount: 5, SilentCount: 580, DormantCount: 444},
		DeadFeeds: 36,
		Quiet:     []storage.QuietFeed{{FeedID: 3927, Title: "Сочи кстати", FeedType: "telegram", MedianGap: 40 * time.Minute, QuietFor: 5*24*time.Hour + 14*time.Hour, Events: 559}},
		Counters:  storage.DailySummaryCounters{PollStateUpdates: 1_500_000, WALBytes: 300 << 20, DBBytes: 600 << 20},
	}
	base := &storage.DailySummaryCounters{PollStateUpdates: 1_000_000, WALBytes: 200 << 20, DBBytes: 590 << 20}
	m := dailySummaryMessage(s, base, until.Add(-24*time.Hour), until.Add(-3*time.Hour))
	for _, want := range []string{
		"Сводка за сутки (2026-11-22, 21:00 UTC)",
		"Опросов: 500 000 (347/мин)",
		"Новых записей: 1 234; неудачных опросов: 93 у 26 лент",
		"telegram: записей 1 234, ошибок 93 у 26 лент",
		"Доставки: отправлено 1 200, ошибок 3, отброшено 1, в очереди 2",
		"всего 2251, в порядке 2200, с ошибкой 36 (исчезли 30, заблокированы 6, мёртвые 36), на паузе 5, тихих 580, спящих 444",
		"База: 600 МБ (+10 МБ за сутки), WAL 100 МБ/сутки",
		"Процесс перезапускался: работает 3 ч 0 мин",
		"Тише обычного (без ошибок, молчат дольше 3× своего ритма и суток): #3927 Сочи кстати (telegram): 5 д 14 ч без записей при обычных 40 мин",
	} {
		if !strings.Contains(m.Plain, want) {
			t.Errorf("missing %q in:\n%s", want, m.Plain)
		}
	}
	if strings.Contains(m.Plain, "  max:") {
		t.Errorf("quiet type must be omitted:\n%s", m.Plain)
	}
	if m.Kind != "daily_summary" || m.Details["polls"] != int64(500000) || !strings.HasPrefix(m.HTML, "<b>") {
		t.Fatalf("kind=%s details=%v", m.Kind, m.Details)
	}

	// First run: no base.
	m = dailySummaryMessage(s, nil, time.Time{}, until.Add(-48*time.Hour))
	if !strings.Contains(m.Plain, "нет вчерашнего снимка") || !strings.Contains(m.Plain, "Без перезапусков: работает 2 д 0 ч") || strings.Contains(m.Plain, "WAL") {
		t.Fatalf("first run:\n%s", m.Plain)
	}
	// Counters reset (negative delta).
	m = dailySummaryMessage(s, &storage.DailySummaryCounters{PollStateUpdates: 9_000_000}, until.Add(-24*time.Hour), time.Time{})
	if !strings.Contains(m.Plain, "счётчики PostgreSQL сброшены") {
		t.Fatalf("reset:\n%s", m.Plain)
	}
}

func TestHumanHelpers(t *testing.T) {
	if groupThousands(-1234567) != "-1 234 567" || groupThousands(999) != "999" {
		t.Fatal(groupThousands(-1234567), groupThousands(999))
	}
	if humanBytes(-(3<<30)) != "-3.0 ГБ" || humanBytes(512) != "512 Б" {
		t.Fatal(humanBytes(-(3 << 30)), humanBytes(512))
	}
	if humanDuration(90*time.Second) != "2 мин" {
		t.Fatal(humanDuration(90 * time.Second))
	}
}
