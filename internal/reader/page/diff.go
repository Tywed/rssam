package page

import (
	"html"
	"strings"
)

// diffHTML renders removed and added lines between two snapshots as a short
// block on top of the entry. Lines are compared as sets in order (LCS), which
// is enough to show what appeared or disappeared on a page.
func diffHTML(oldText, newText string) string {
	oldLines := splitLines(oldText)
	newLines := splitLines(newText)
	keep := lcsKeep(oldLines, newLines)
	var removed, added []string
	i, j := 0, 0
	for _, k := range keep {
		for ; i < k[0]; i++ {
			removed = append(removed, oldLines[i])
		}
		for ; j < k[1]; j++ {
			added = append(added, newLines[j])
		}
		i, j = k[0]+1, k[1]+1
	}
	removed = append(removed, oldLines[i:]...)
	added = append(added, newLines[j:]...)
	if len(removed) == 0 && len(added) == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString(`<div class="page-diff">`)
	writeLines(&b, "ins", added)
	writeLines(&b, "del", removed)
	b.WriteString(`</div><hr>`)
	return b.String()
}

func writeLines(b *strings.Builder, tag string, lines []string) {
	const maxLines = 50
	for n, line := range lines {
		if n == maxLines {
			b.WriteString("<p>…</p>")
			return
		}
		b.WriteString("<p><")
		b.WriteString(tag)
		b.WriteString(">")
		b.WriteString(html.EscapeString(line))
		b.WriteString("</")
		b.WriteString(tag)
		b.WriteString("></p>")
	}
}

func splitLines(s string) []string {
	if s == "" {
		return nil
	}
	return strings.Split(s, "\n")
}

// lcsKeep returns index pairs of a longest common subsequence of a and b.
// Inputs are capped so the table stays small (snapshots are ≤32 KB anyway).
func lcsKeep(a, b []string) [][2]int {
	const maxN = 2000
	if len(a) > maxN {
		a = a[:maxN]
	}
	if len(b) > maxN {
		b = b[:maxN]
	}
	n, m := len(a), len(b)
	if n == 0 || m == 0 {
		return nil
	}
	dp := make([][]int32, n+1)
	for i := range dp {
		dp[i] = make([]int32, m+1)
	}
	for i := n - 1; i >= 0; i-- {
		for j := m - 1; j >= 0; j-- {
			if a[i] == b[j] {
				dp[i][j] = dp[i+1][j+1] + 1
			} else {
				dp[i][j] = max(dp[i+1][j], dp[i][j+1])
			}
		}
	}
	var out [][2]int
	i, j := 0, 0
	for i < n && j < m {
		switch {
		case a[i] == b[j]:
			out = append(out, [2]int{i, j})
			i++
			j++
		case dp[i+1][j] >= dp[i][j+1]:
			i++
		default:
			j++
		}
	}
	return out
}
