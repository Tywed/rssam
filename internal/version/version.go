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
// git describe (0.1.0-1-gabcdef) is newer than tag 0.1.0 and older than 0.1.1.
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
	return compareSemverSuffix(a, b)
}

func compareSemverSuffix(a, b string) int {
	aRest := semverSuffix(a)
	bRest := semverSuffix(b)
	if aRest == bRest {
		return 0
	}
	aN, aGit := gitDescribeAhead(aRest)
	bN, bGit := gitDescribeAhead(bRest)
	switch {
	case aGit && bGit:
		if aN < bN {
			return -1
		}
		if aN > bN {
			return 1
		}
		return 0
	case aGit && bRest == "":
		return 1
	case bGit && aRest == "":
		return -1
	case aRest == "":
		return 1
	case bRest == "":
		return -1
	case aGit:
		return 1
	case bGit:
		return -1
	default:
		return strings.Compare(aRest, bRest)
	}
}

func semverSuffix(s string) string {
	s = strings.SplitN(s, "+", 2)[0]
	if i := strings.IndexByte(s, '-'); i >= 0 {
		return s[i+1:]
	}
	return ""
}

func gitDescribeAhead(rest string) (int, bool) {
	if rest == "" {
		return 0, false
	}
	n, i := 0, 0
	for i < len(rest) && rest[i] >= '0' && rest[i] <= '9' {
		n = n*10 + int(rest[i]-'0')
		i++
	}
	if i == 0 || i >= len(rest) || rest[i] != '-' {
		return 0, false
	}
	rest = rest[i+1:]
	if !strings.HasPrefix(rest, "g") {
		return 0, false
	}
	hex := strings.TrimPrefix(rest, "g")
	hex = strings.TrimSuffix(hex, "-dirty")
	if hex == "" {
		return 0, false
	}
	for _, c := range hex {
		if (c < '0' || c > '9') && (c < 'a' || c > 'f') && (c < 'A' || c > 'F') {
			return 0, false
		}
	}
	return n, true
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
