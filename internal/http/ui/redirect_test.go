package ui

import "testing"

func TestLocalUIPath(t *testing.T) {
	cases := map[string]string{
		"":                                   "/ui/fallback",
		"/ui/unread":                         "/ui/unread",
		"/ui/unread?entry_id=5&x=y":          "/ui/unread?entry_id=5&x=y",
		"https://rss.example/ui/feeds/3":     "/ui/feeds/3",
		"//evil.example/ui/unread":           "/ui/unread",
		"https://evil.example/":              "/ui/fallback",
		"https://evil.example/ui":            "/ui/fallback",
		"/v1/feeds":                          "/ui/fallback",
		"/ui/../etc":                         "/ui/fallback",
		"javascript:alert(1)":                "/ui/fallback",
		"https://evil.example\\@rss/ui/x":    "/ui/fallback",
		"::bad url::":                        "/ui/fallback",
		"/ui/unread#frag":                    "/ui/unread",
		"https://rss.example/ui/x?next=//e/": "/ui/x?next=//e/",
		"/ui/../../ui/feeds":                 "/ui/feeds",
		"/ui/./feeds/":                       "/ui/feeds/",
		"/ui/%2e%2e/admin":                   "/ui/fallback",
	}
	for in, want := range cases {
		if got := localUIPath(in, "/ui/fallback"); got != want {
			t.Errorf("localUIPath(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestSafeCSSColor(t *testing.T) {
	ok := []string{"#fff", "#FFF", "#ffff", "#2980b9", "#2980B9ff", " #abc "}
	for _, v := range ok {
		if got := safeCSSColor(v); got == "" {
			t.Errorf("safeCSSColor(%q) rejected", v)
		}
	}
	bad := []string{"", "red", "#ggg", "#12345", "#fff;background:url(x)", "url(x)", "#fff}", "expression(1)"}
	for _, v := range bad {
		if got := safeCSSColor(v); got != "" {
			t.Errorf("safeCSSColor(%q) = %q, want rejection", v, got)
		}
	}
}
