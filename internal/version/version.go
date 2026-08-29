package version

import "runtime"

var (
	// Version задаётся при сборке через -ldflags.
	Version = "dev"
	Commit  = "none"
	Date    = "unknown"
)

func String() string {
	return "rssam " + Version + " (" + Commit + ", " + Date + ", " + runtime.Version() + ")"
}
