package ui

import (
	"testing"
	"time"

	"rssam/internal/storage"
)

func TestClassifyAdminWebhookStatus(t *testing.T) {
	now := time.Now()
	lastSent := now.Add(-time.Hour)

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
			name: "dead",
			row: storage.AdminWebhookRow{
				Webhook:   storage.Webhook{Enabled: true},
				DeadCount: 1,
			},
			want: "dead",
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
		{Status: "dead"},
		{Status: "ok"},
	}
	filtered := filterAdminWebhookRows(rows, "ok")
	if len(filtered) != 2 {
		t.Fatalf("len = %d, want 2", len(filtered))
	}
	if len(filterAdminWebhookRows(rows, "all")) != 3 {
		t.Fatal("all filter should keep all rows")
	}
}
