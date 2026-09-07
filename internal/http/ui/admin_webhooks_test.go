package ui

import (
	"testing"
	"time"

	"rssam/internal/storage"
)

func TestClassifyAdminWebhookStatus(t *testing.T) {
	now := time.Now()
	lastSent := now.Add(-time.Hour)
	beforeSent := now.Add(-2 * time.Hour)
	afterSent := now.Add(-30 * time.Minute)

	tests := []struct {
		name string
		row  storage.AdminWebhookRow
		want string
	}{
		{
			name: "disabled",
			row:  storage.AdminWebhookRow{Webhook: storage.Webhook{Enabled: false}},
			want: "disabled",
		},
		{
			name: "disabled wins over error",
			row: storage.AdminWebhookRow{
				Webhook:      storage.Webhook{Enabled: false},
				FailedCount:  1,
				LastFailedAt: &afterSent,
			},
			want: "disabled",
		},
		{
			name: "error: failed, never sent",
			row: storage.AdminWebhookRow{
				Webhook:      storage.Webhook{Enabled: true},
				FailedCount:  1,
				LastFailedAt: &afterSent,
			},
			want: "error",
		},
		{
			name: "error: failure after last success",
			row: storage.AdminWebhookRow{
				Webhook:      storage.Webhook{Enabled: true},
				SentCount:    10,
				FailedCount:  1,
				LastSentAt:   &lastSent,
				LastFailedAt: &afterSent,
			},
			want: "error",
		},
		{
			name: "error wins over queue",
			row: storage.AdminWebhookRow{
				Webhook:       storage.Webhook{Enabled: true},
				FailedCount:   1,
				LastFailedAt:  &afterSent,
				QueueDueCount: 3,
			},
			want: "error",
		},
		{
			name: "ok: success after earlier failure",
			row: storage.AdminWebhookRow{
				Webhook:      storage.Webhook{Enabled: true},
				SentCount:    10,
				FailedCount:  3,
				LastSentAt:   &lastSent,
				LastFailedAt: &beforeSent,
			},
			want: "ok",
		},
		{
			name: "ok: historical failures, only counters left after reset of nothing",
			row: storage.AdminWebhookRow{
				Webhook:     storage.Webhook{Enabled: true},
				SentCount:   1,
				FailedCount: 5,
				LastSentAt:  &lastSent,
			},
			want: "ok",
		},
		{
			name: "queue",
			row: storage.AdminWebhookRow{
				Webhook:       storage.Webhook{Enabled: true},
				QueueDueCount: 2,
			},
			want: "queue",
		},
		{
			name: "retrying",
			row: storage.AdminWebhookRow{
				Webhook:       storage.Webhook{Enabled: true},
				RetryingCount: 1,
			},
			want: "retrying",
		},
		{
			name: "idle",
			row: storage.AdminWebhookRow{
				Webhook: storage.Webhook{Enabled: true},
			},
			want: "idle",
		},
		{
			name: "ok",
			row: storage.AdminWebhookRow{
				Webhook:    storage.Webhook{Enabled: true},
				SentCount:  5,
				LastSentAt: &lastSent,
			},
			want: "ok",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := classifyAdminWebhookStatus(tc.row); got != tc.want {
				t.Fatalf("classifyAdminWebhookStatus() = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestClassifyWebhookError(t *testing.T) {
	if got := classifyWebhookError("non-2xx status: 503"); got != "HTTP" {
		t.Fatalf("got %q", got)
	}
	if got := classifyWebhookError("context deadline exceeded"); got != "Сеть" {
		t.Fatalf("got %q", got)
	}
	if got := classifyWebhookError("invalid body_template: bad"); got != "Шаблон" {
		t.Fatalf("got %q", got)
	}
}

func TestFilterAdminWebhookRows(t *testing.T) {
	rows := []adminWebhookRowView{
		{Status: "ok"},
		{Status: "error"},
		{Status: "ok"},
	}
	filtered := filterAdminWebhookRows(rows, "ok")
	if len(filtered) != 2 {
		t.Fatalf("len = %d, want 2", len(filtered))
	}
	if len(filterAdminWebhookRows(rows, "error")) != 1 {
		t.Fatal("error filter should keep one row")
	}
	if len(filterAdminWebhookRows(rows, "all")) != 3 {
		t.Fatal("all filter should keep all rows")
	}
	counts := countAdminWebhookStatuses(rows)
	if counts.OK != 2 || counts.Error != 1 {
		t.Fatalf("counts = %+v", counts)
	}
}

func TestAdminWebhookStatusLabels(t *testing.T) {
	for _, st := range adminWebhookStatuses {
		if !isAdminWebhookStatus(st) {
			t.Fatalf("%q must be a valid status", st)
		}
		if adminWebhookStatusLabel(st) == st {
			t.Fatalf("status %q has no label", st)
		}
	}
	if isAdminWebhookStatus("dead") {
		t.Fatal("legacy status \"dead\" must not be accepted as a filter")
	}
	if got := adminWebhookStatusLabel("error"); got != "Ошибки" {
		t.Fatalf("label = %q", got)
	}
	if got := adminWebhookStatusClass("error"); got != "feed-status-error" {
		t.Fatalf("class = %q", got)
	}
}

func TestWebhookSuccessRate(t *testing.T) {
	if got := webhookSuccessRate(0, 0); got != "—" {
		t.Fatalf("empty = %q", got)
	}
	if got := webhookSuccessRate(3, 1); got != "75%" {
		t.Fatalf("3/1 = %q", got)
	}
	if got := webhookSuccessRate(0, 4); got != "0%" {
		t.Fatalf("0/4 = %q", got)
	}
}
