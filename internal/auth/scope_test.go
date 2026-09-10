package auth

import "testing"

func TestScopeAllows(t *testing.T) {
	cases := []struct {
		scope, method, path string
		want                bool
	}{
		{ScopeRead, "GET", "/v1/entries", true},
		{ScopeRead, "PUT", "/v1/entries", false},
		{ScopeRead, "POST", "/v1/feeds", false},
		{ScopeWrite, "PUT", "/v1/entries", true},
		{ScopeWrite, "PUT", "/v1/feeds/3/entries/9", true},
		{ScopeWrite, "PUT", "/v1/feeds/3/mark-all-as-read", true},
		{ScopeWrite, "PUT", "/v1/categories/2/mark-all-as-read", true},
		{ScopeWrite, "PUT", "/v1/feeds/3", false},
		{ScopeWrite, "POST", "/v1/feeds", false},
		{ScopeWrite, "POST", "/v1/feeds/3/refresh", false},
		{ScopeWrite, "DELETE", "/v1/feeds/3", false},
		{ScopeWrite, "PUT", "/v1/me", false},
		{ScopeWrite, "POST", "/v1/me/api-keys", false},
		{ScopeAdmin, "DELETE", "/v1/users/2", true},
		{"", "POST", "/v1/feeds", true},
		{"bogus", "GET", "/v1/entries", false},
	}
	for _, c := range cases {
		if got := ScopeAllows(c.scope, c.method, c.path); got != c.want {
			t.Errorf("ScopeAllows(%q, %s %s) = %v, want %v", c.scope, c.method, c.path, got, c.want)
		}
	}
}
