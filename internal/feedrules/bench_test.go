package feedrules

import (
	"regexp"
	"strings"
	"testing"
)

const benchBlocked = "(?i)реклама|промо|скидк\\w+\n(?i)sponsored\n^\\[AD\\]"
const benchRewrite = "\\?utm_\\w+=[^&]+&?\n\n/amp/(\\d+)\n/article/$1"

func BenchmarkCompileEveryPoll(b *testing.B) {
	for b.Loop() {
		lines := strings.Split(benchBlocked, "\n")
		_, _ = regexp.Compile("(?:" + strings.Join(lines, ")|(?:") + ")")
		_, _ = CompileRewrites(benchRewrite)
	}
}

func BenchmarkCached(b *testing.B) {
	for b.Loop() {
		_ = Cached(benchBlocked, "", benchRewrite)
	}
}
