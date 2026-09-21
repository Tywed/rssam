package auth

import "testing"

func TestRoleAtLeast(t *testing.T) {
	cases := []struct {
		have, want string
		ok         bool
	}{
		{RoleAdmin, RoleAdmin, true},
		{RoleAdmin, RoleReader, true},
		{RoleEditor, RoleEditor, true},
		{RoleEditor, RoleAdmin, false},
		{RoleReader, RoleEditor, false},
		{RoleReader, RoleReader, true},
		{"", RoleReader, false},
		{"root", RoleReader, false},
	}
	for _, c := range cases {
		if got := RoleAtLeast(c.have, c.want); got != c.ok {
			t.Errorf("RoleAtLeast(%q, %q) = %v, want %v", c.have, c.want, got, c.ok)
		}
	}
	if IsValidRole("") || IsValidRole("root") || !IsValidRole(RoleEditor) {
		t.Fatal("IsValidRole")
	}
	if p := (Principal{Role: RoleEditor}); p.IsAdmin() || !p.CanEdit() {
		t.Fatalf("editor principal: %+v", p)
	}
	if p := (Principal{Role: RoleReader}); p.CanEdit() {
		t.Fatalf("reader principal: %+v", p)
	}
}
