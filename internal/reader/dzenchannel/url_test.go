package dzenchannel

import (
	"reflect"
	"testing"
)

func TestParse(t *testing.T) {
	cases := []struct {
		in   string
		want Channel
		ok   bool
	}{
		{"https://dzen.ru/tass", Channel{Name: "tass"}, true},
		{"https://dzen.ru/tass/", Channel{Name: "tass"}, true},
		{"https://www.dzen.ru/rbc.ru?utm_source=x", Channel{Name: "rbc.ru"}, true},
		{"https://zen.yandex.ru/hi-tech.mail.ru", Channel{Name: "hi-tech.mail.ru"}, true},
		{"https://dzen.ru/id/5f9abb2e66afb7042d59a0af", Channel{ID: "5f9abb2e66afb7042d59a0af"}, true},
		{"dzen-channel://tass", Channel{Name: "tass"}, true},
		{"dzen-channel://id/5f9abb2e66afb7042d59a0af", Channel{ID: "5f9abb2e66afb7042d59a0af"}, true},
		{"https://dzen.ru/tass?types=long_video,article", Channel{Name: "tass", Types: []string{"article", "long_video"}}, true},
		{"dzen-channel://tass?types=short_video", Channel{Name: "tass", Types: []string{"short_video"}}, true},
		{"https://dzen.ru/tass?types=", Channel{Name: "tass"}, true},
		{"https://dzen.ru/tass?types=podcast", Channel{}, false},
		{"https://dzen.ru/news/search?text=python", Channel{}, false},
		{"https://dzen.ru/a/asPgHIhyeVtzCmCb", Channel{}, false},
		{"https://dzen.ru/video/watch/6abcab9fa95eb9133bce49ab", Channel{}, false},
		{"https://dzen.ru/id/notahexid", Channel{}, false},
		{"https://dzen.ru/id/5f9abb2e66afb7042d59a0af/extra", Channel{}, false},
		{"https://dzen.ru/", Channel{}, false},
		{"https://dzen.ru/tass/articles", Channel{}, false},
		{"https://example.com/tass", Channel{}, false},
		{"rutube-person://123", Channel{}, false},
		{"", Channel{}, false},
	}
	for _, c := range cases {
		got, ok := Parse(c.in)
		if ok != c.ok || !reflect.DeepEqual(got, c.want) {
			t.Errorf("Parse(%q) = %+v %v, want %+v %v", c.in, got, ok, c.want, c.ok)
		}
	}
}

func TestChannelStringsAndParams(t *testing.T) {
	named := Channel{Name: "tass"}
	if named.String() != "tass" || named.PageURL() != "https://dzen.ru/tass" {
		t.Fatalf("named: %q %q", named.String(), named.PageURL())
	}
	if k, v := named.queryParam(); k != "channel_name" || v != "tass" {
		t.Fatalf("named param %s=%s", k, v)
	}
	byID := Channel{ID: "5f9abb2e66afb7042d59a0af"}
	if byID.String() != "id/5f9abb2e66afb7042d59a0af" || byID.PageURL() != "https://dzen.ru/id/5f9abb2e66afb7042d59a0af" {
		t.Fatalf("by id: %q %q", byID.String(), byID.PageURL())
	}
	if k, v := byID.queryParam(); k != "channel_id" || v != "5f9abb2e66afb7042d59a0af" {
		t.Fatalf("id param %s=%s", k, v)
	}
}
