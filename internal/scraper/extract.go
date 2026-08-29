package scraper

import (
	"fmt"
	"strings"

	"github.com/PuerkitoBio/goquery"
)

// ExtractContent extracts main article HTML from a page.
// scraperRules: Miniflux-style "key=selector" lines or plain CSS selectors (content).
func ExtractContent(html string, scraperRules string) (string, error) {
	html = strings.TrimSpace(html)
	if html == "" {
		return "", fmt.Errorf("empty html")
	}

	if content := extractWithRules(html, scraperRules); content != "" {
		return content, nil
	}

	doc, err := goquery.NewDocumentFromReader(strings.NewReader(html))
	if err != nil {
		return "", fmt.Errorf("parse html: %w", err)
	}
	for _, sel := range []string{"article", "main", "[role=main]", ".post-content", ".entry-content", "#content"} {
		if c := extractSelectorHTML(doc, sel); c != "" {
			return c, nil
		}
	}
	if body := doc.Find("body").First(); body.Length() > 0 {
		if c, err := body.Html(); err == nil && strings.TrimSpace(c) != "" {
			return strings.TrimSpace(c), nil
		}
	}
	return "", fmt.Errorf("no content extracted")
}

func extractWithRules(html, scraperRules string) string {
	rules := parseScraperRules(scraperRules)
	if len(rules) == 0 && strings.TrimSpace(scraperRules) == "" {
		return ""
	}

	doc, err := goquery.NewDocumentFromReader(strings.NewReader(html))
	if err != nil {
		return ""
	}

	if sel, ok := rules["content"]; ok && sel != "" {
		if c := extractSelectorHTML(doc, sel); c != "" {
			return c
		}
	}
	if sel, ok := rules["article"]; ok && sel != "" {
		if c := extractSelectorHTML(doc, sel); c != "" {
			return c
		}
	}

	for _, line := range strings.Split(scraperRules, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.Contains(line, "=") {
			continue
		}
		if c := extractSelectorHTML(doc, line); c != "" {
			return c
		}
	}
	return ""
}

func parseScraperRules(raw string) map[string]string {
	out := map[string]string{}
	for _, line := range strings.Split(raw, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		key, val, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		key = strings.TrimSpace(strings.ToLower(key))
		val = strings.TrimSpace(val)
		if key != "" && val != "" {
			out[key] = val
		}
	}
	return out
}

func extractSelectorHTML(doc *goquery.Document, selector string) string {
	selection := doc.Find(selector).First()
	if selection.Length() == 0 {
		return ""
	}
	html, err := selection.Html()
	if err != nil || strings.TrimSpace(html) == "" {
		return ""
	}
	return strings.TrimSpace(html)
}
