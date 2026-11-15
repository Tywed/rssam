package bridgeconfig

import "testing"

func TestStoredValidate(t *testing.T) {
	if err := (Stored{}).Validate(); err != nil {
		t.Fatal(err)
	}
	ok := Stored{}
	ok.Telegram.StaticProxy = "socks5://user:pw@[::1]:1080"
	ok.Telegram.ConnectTimeout = "10s"
	ok.Max.APIBaseURL = "https://apimax.example"
	if err := ok.Validate(); err != nil {
		t.Fatal(err)
	}
	for name, bad := range map[string]Stored{
		"duration":   func() Stored { s := Stored{}; s.Telegram.RequestTimeout = "25 sec"; return s }(),
		"proxy port": func() Stored { s := Stored{}; s.Telegram.StaticProxy = "1.2.3.4:70000"; return s }(),
		"url scheme": func() Stored { s := Stored{}; s.Max.APIBaseURL = "apimax.example"; return s }(),
		"negative":   func() Stored { s := Stored{}; s.VK.DefaultCount = -1; return s }(),
	} {
		if err := bad.Validate(); err == nil {
			t.Fatalf("%s: expected error", name)
		}
	}
}
