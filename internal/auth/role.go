package auth

// User roles, ordered: reader ⊂ editor ⊂ admin. A reader subscribes, reads
// and stars; an editor additionally manages the feed catalog (feeds, OPML,
// categories, filters, webhooks); an admin also manages users and the
// instance.
const (
	RoleReader = "reader"
	RoleEditor = "editor"
	RoleAdmin  = "admin"
)

var roleRank = map[string]int{RoleReader: 1, RoleEditor: 2, RoleAdmin: 3}

func IsValidRole(role string) bool {
	_, ok := roleRank[role]
	return ok
}

// RoleAtLeast reports whether have grants everything want grants. An
// unknown role grants nothing.
func RoleAtLeast(have, want string) bool {
	h, ok := roleRank[have]
	return ok && h >= roleRank[want]
}
