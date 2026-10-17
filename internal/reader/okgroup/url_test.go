package okgroup

import "testing"

func TestParse(t *testing.T) {
	ok := map[string]Group{
		"https://ok.ru/group/58904363073576":   {ID: "58904363073576"},
		"https://ok.ru/rianovosti":             {Slug: "rianovosti"},
		"https://www.ok.ru/rianovosti/":        {Slug: "rianovosti"},
		"https://m.ok.ru/group/58904363073576": {ID: "58904363073576"},
		"ok-group://58904363073576":            {ID: "58904363073576"},
		"ok-group://rianovosti":                {Slug: "rianovosti"},
	}
	for in, want := range ok {
		got, found := Parse(in)
		if !found || got != want {
			t.Fatalf("%s: got %+v found=%v", in, got, found)
		}
	}
	for _, in := range []string{
		"", "https://ok.ru/", "https://ok.ru/profile/348138300", "https://ok.ru/group/58904363073576/topic/157975841086248",
		"https://ok.ru/video/123", "https://ok.ru/dk?cmd=x", "https://vk.com/rianovosti", "https://ok.ru/feed", "https://ok.ru/a",
	} {
		if _, found := Parse(in); found {
			t.Fatalf("%s must not parse", in)
		}
	}
	if (Group{Slug: "x"}).PageURL() != "https://ok.ru/x" || (Group{ID: "1234567"}).PageURL() != "https://ok.ru/group/1234567" {
		t.Fatal("PageURL")
	}
	if TopicURL("1", "2") != "https://ok.ru/group/1/topic/2" {
		t.Fatal("TopicURL")
	}
}
