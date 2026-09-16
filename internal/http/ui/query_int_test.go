package ui

import (
	"net/http/httptest"
	"testing"
)

func TestQueryIntHelpers(t *testing.T) {
	cases := []struct {
		url                 string
		limit, offset, page int
	}{
		{"/x", defaultPageLimit, 0, 1},
		{"/x?limit=10&offset=5&page=3", 10, 5, 3},
		{"/x?limit=0&offset=-1&page=0", defaultPageLimit, 0, 1},
		{"/x?limit=101&offset=abc&page=-2", defaultPageLimit, 0, 1},
		{"/x?limit=%2B7&offset=%2B3&page=+2", 7, 3, 1},
		{"/x?limit=1e2&page=99999999999999999999", defaultPageLimit, 0, 1},
	}
	for _, tc := range cases {
		r := httptest.NewRequest("GET", tc.url, nil)
		limit, offset := parsePage(r)
		if limit != tc.limit || offset != tc.offset {
			t.Errorf("%s: parsePage = %d,%d want %d,%d", tc.url, limit, offset, tc.limit, tc.offset)
		}
		l, off, page := pageOffset(r, 50)
		if l != 50 || page != tc.page || off != (tc.page-1)*50 {
			t.Errorf("%s: pageOffset = %d,%d,%d want 50,%d,%d", tc.url, l, off, page, (tc.page-1)*50, tc.page)
		}
	}
}
