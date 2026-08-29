package version

import (
	"runtime"
	"strings"
)

var (
	Version = "dev"
	Commit  = "none"
	Date    = "unknown"
)

const DefaultGitHubRepo = "Tywed/rssam"

func String() string {
	return "rssam " + Version + " (" + Commit + ", " + Date + ", " + runtime.Version() + ")"
}

func Normalize(v string) string {
	v = strings.TrimSpace(v)
	return strings.TrimPrefix(v, "v")
}

// CompareSemver returns -1 if a<b, 0 if equal, 1 if a>b. Non-semver (dev) compares as less than a tagged release.
func CompareSemver(a, b string) int {
	a, b = Normalize(a), Normalize(b)
	if a == b {
		return 0
	}
	as, aok := parseSemver(a)
	bs, bok := parseSemver(b)
	if !aok && !bok {
		return strings.Compare(a, b)
	}
	if !aok {
		return -1
	}
	if !bok {
		return 1
	}
	for i := 0; i < 3; i++ {
		if as[i] < bs[i] {
			return -1
		}
		if as[i] > bs[i] {
			return 1
		}
	}
	return 0
}

func parseSemver(s string) ([3]int, bool) {
	var out [3]int
	s = strings.SplitN(s, "-", 2)[0]
	s = strings.SplitN(s, "+", 2)[0]
	parts := strings.Split(s, ".")
	if len(parts) < 1 || len(parts) > 3 {
		return out, false
	}
	for i, p := range parts {
		n := 0
		if p == "" {
			return out, false
		}
		for _, c := range p {
			if c < '0' || c > '9' {
				return out, false
			}
			n = n*10 + int(c-'0')
		}
		out[i] = n
	}
	return out, true
}
