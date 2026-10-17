package okgroup

import (
	"os"
	"testing"
	"time"
)

func read(t *testing.T, name string) []byte {
	t.Helper()
	b, err := os.ReadFile("testdata/" + name)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func TestParseGroupPage(t *testing.T) {
	page, err := parseGroupPage(read(t, "group.html"))
	if err != nil {
		t.Fatal(err)
	}
	if page.GroupID != "58904363073576" || page.Title != "Информационное агентство ЯНАО" || page.Profile {
		t.Fatalf("page = %+v", page)
	}
	if len(page.Posts) != 3 {
		t.Fatalf("posts = %d", len(page.Posts))
	}
	p := page.Posts[0]
	if p.ID != "157975841086248" || p.Title != "В Надыме почти готова новая заправка" || p.When != "15:59" || p.Image == "" {
		t.Fatalf("post = %+v", p)
	}
	// The list cuts the text to the headline plus "…"; nothing else is left.
	if p.Text != "" {
		t.Fatalf("list text = %q", p.Text)
	}
	if page.Posts[1].Image != "" || page.Posts[1].ID != "157975749663528" {
		t.Fatalf("post[1] = %+v", page.Posts[1])
	}

	kp, err := parseGroupPage(read(t, "group_kinopoisk.html"))
	if err != nil || kp.GroupID != "52728120934492" || kp.Title != "Кинопоиск" || len(kp.Posts) != 2 {
		t.Fatalf("kinopoisk: %+v %v", kp, err)
	}
	if kp.Posts[0].When != "12 фев" || kp.Posts[0].Title != "Зумеры тут?" {
		t.Fatalf("pinned post = %+v", kp.Posts[0])
	}

	prof, err := parseGroupPage(read(t, "profile.html"))
	if err != nil || !prof.Profile || prof.GroupID != "" {
		t.Fatalf("profile: %+v %v", prof, err)
	}
}

func TestParseTopicPage(t *testing.T) {
	tp, ok := parseTopicPage(read(t, "topic.html"))
	if !ok {
		t.Fatal("no Article in JSON-LD")
	}
	if tp.Headline != "В Надыме почти готова новая заправка" || len(tp.Images) != 1 {
		t.Fatalf("topic = %+v", tp)
	}
	want := time.Date(2026, 10, 16, 12, 59, 2, 0, time.UTC)
	if !tp.Published.Equal(want) {
		t.Fatalf("published = %s", tp.Published)
	}
	if len(tp.Body) < 300 || tp.Body[:len("В Надыме")] != "В Надыме" {
		t.Fatalf("body = %q", tp.Body)
	}
	if _, ok := parseTopicPage([]byte("<html><body>nothing</body></html>")); ok {
		t.Fatal("page without JSON-LD must report !ok")
	}
}

func TestParseWhen(t *testing.T) {
	now := time.Date(2026, 10, 16, 18, 36, 0, 0, time.UTC) // 21:36 MSK
	cases := map[string]string{
		"15:59":        "2026-10-16T12:59:00Z",
		"вчера 08:02":  "2026-10-15T05:02:00Z",
		"12 фев":       "2026-02-11T21:00:00Z",
		"12 окт":       "2026-10-11T21:00:00Z",
		"20 дек":       "2025-12-19T21:00:00Z", // still in the future this year
		"3 мая 2024":   "2024-05-02T21:00:00Z",
		"1 янв 14:05":  "2026-01-01T11:05:00Z",
		"сегодня 9:05": "2026-10-16T06:05:00Z",
	}
	for in, want := range cases {
		got := parseWhen(in, now)
		if got == nil || got.Format(time.RFC3339) != want {
			t.Fatalf("%q: got %v want %s", in, got, want)
		}
	}
	for _, in := range []string{"", "x", "25:00", "32 окт", "вчера", "12 xyz"} {
		if got := parseWhen(in, now); got != nil {
			t.Fatalf("%q: got %v", in, got)
		}
	}
}
