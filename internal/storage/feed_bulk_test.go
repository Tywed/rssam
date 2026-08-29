package storage

import "testing"

func TestValidateBulkFeedUpdate(t *testing.T) {
	interval := 30
	hash := true
	pause := false

	tests := []struct {
		name    string
		update  BulkFeedUpdate
		wantErr bool
	}{
		{"empty", BulkFeedUpdate{}, true},
		{"interval ok", BulkFeedUpdate{IntervalMinutes: &interval}, false},
		{"interval low", BulkFeedUpdate{IntervalMinutes: ptrInt(0)}, true},
		{"webhook set", BulkFeedUpdate{WebhookSet: true}, false},
		{"hash only", BulkFeedUpdate{StoreHashOnly: &hash}, false},
		{"pause", BulkFeedUpdate{ManualPaused: &pause}, false},
		{"move", BulkFeedUpdate{MoveCategory: true}, false},
		{"multiple", BulkFeedUpdate{IntervalMinutes: &interval, ManualPaused: &pause}, true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			err := validateBulkFeedUpdate(tc.update)
			if (err != nil) != tc.wantErr {
				t.Fatalf("validateBulkFeedUpdate() err=%v wantErr=%v", err, tc.wantErr)
			}
		})
	}
}

func ptrInt(v int) *int { return &v }
