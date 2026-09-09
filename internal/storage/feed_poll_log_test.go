package storage

import "testing"

func TestDecidePollLog(t *testing.T) {
	fail := &FeedPollLogEntry{OK: false, Error: "boom"}
	ok := &FeedPollLogEntry{OK: true, Error: ""}
	cases := []struct {
		name   string
		latest *FeedPollLogEntry
		ok     bool
		err    string
		want   pollLogAction
	}{
		{name: "first success", ok: true, want: pollLogInsert},
		{name: "first failure", ok: false, err: "x", want: pollLogInsert},
		{name: "success after success", latest: ok, ok: true, want: pollLogSkip},
		{name: "same failure", latest: fail, err: "boom", want: pollLogCoalesce},
		{name: "failure then success", latest: fail, ok: true, want: pollLogInsert},
		{name: "success then failure", latest: ok, err: "boom", want: pollLogInsert},
		{name: "different failure", latest: fail, err: "other", want: pollLogInsert},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := decidePollLog(tc.latest, tc.ok, tc.err); got != tc.want {
				t.Fatalf("got %v want %v", got, tc.want)
			}
		})
	}
}
