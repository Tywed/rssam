package okgroup

import (
	"bytes"
	"encoding/json"
	"html"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/PuerkitoBio/goquery"
)

// groupPage is what the bridge reads from the desktop group page.
type groupPage struct {
	GroupID string
	Title   string
	// Profile is set when the address opened a user profile, not a group.
	Profile bool
	Posts   []listPost
}

// listPost is one feed block of the group page: enough for an entry when
// the topic page is not fetched. When is the label of <time> ("15:59",
// "вчера 08:02", "12 фев", "12 фев 2024"), Moscow time, minute precision
// for today/yesterday and day precision for older posts.
type listPost struct {
	ID    string
	Title string
	Text  string
	Image string
	Link  string
	When  string
}

var (
	ogURLGroupRe = regexp.MustCompile(`^https?://(?:www\.)?ok\.ru/group/([0-9]+)`)
	topicIDRe    = regexp.MustCompile(`"mediaTopicId":"([0-9]+)"`)
)

func parseGroupPage(body []byte) (*groupPage, error) {
	doc, err := goquery.NewDocumentFromReader(bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	page := &groupPage{}
	ogURL, _ := doc.Find(`meta[property="og:url"]`).Attr("content")
	if m := ogURLGroupRe.FindStringSubmatch(strings.TrimSpace(ogURL)); m != nil {
		page.GroupID = m[1]
	} else if strings.Contains(ogURL, "/profile/") {
		page.Profile = true
	}
	if t, ok := doc.Find(`meta[property="og:title"]`).Attr("content"); ok {
		page.Title = pageTitle(t)
	}
	if page.Title == "" {
		page.Title = pageTitle(doc.Find("title").First().Text())
	}
	doc.Find("div.feed-w").Each(func(_ int, s *goquery.Selection) {
		if p, ok := parseListPost(s); ok {
			page.Posts = append(page.Posts, p)
		}
	})
	return page, nil
}

// pageTitle cuts the "| Группа на OK.ru | …" tail of the page title.
func pageTitle(t string) string {
	t = strings.TrimSpace(t)
	if i := strings.Index(t, " | "); i > 0 {
		t = t[:i]
	}
	return strings.TrimSpace(t)
}

func parseListPost(s *goquery.Selection) (listPost, bool) {
	p := listPost{}
	if tid, ok := s.Find(".media-text_cnt_tx[data-tid]").First().Attr("data-tid"); ok {
		p.ID = strings.TrimSpace(tid)
	}
	if p.ID == "" {
		s.Find("[data-seen-params]").EachWithBreak(func(_ int, el *goquery.Selection) bool {
			raw, _ := el.Attr("data-seen-params")
			if m := topicIDRe.FindStringSubmatch(html.UnescapeString(raw)); m != nil {
				p.ID = m[1]
				return false
			}
			return true
		})
	}
	if p.ID == "" {
		return p, false
	}
	p.Title = strings.TrimSpace(s.Find(".media-text_heading-v2, .media-text_heading").First().Text())
	text := s.Find(".media-text_cnt_tx").First()
	text.Find("h2, h3").Remove()
	text.Find("br").ReplaceWithHtml("\n")
	p.Text = cleanText(text.Text())
	if src, ok := s.Find("img.collage_img").First().Attr("src"); ok {
		p.Image = strings.TrimSpace(src)
	}
	if href, ok := s.Find("a.media-link_a[href]").First().Attr("href"); ok && strings.HasPrefix(href, "http") {
		p.Link = strings.TrimSpace(href)
	}
	p.When = strings.TrimSpace(s.Find("time").First().Text())
	return p, true
}

// cleanText collapses the whitespace of a feed block and drops the "…"
// the page appends to a cut text.
func cleanText(t string) string {
	lines := strings.Split(t, "\n")
	out := lines[:0]
	for _, l := range lines {
		l = strings.Join(strings.Fields(l), " ")
		if l != "" {
			out = append(out, l)
		}
	}
	t = strings.Join(out, "\n")
	return strings.TrimSpace(strings.TrimSuffix(t, "..."))
}

// topic is the JSON-LD Article of a topic page.
type topic struct {
	Headline  string
	Body      string
	Published time.Time
	Images    []string
}

func parseTopicPage(body []byte) (*topic, bool) {
	doc, err := goquery.NewDocumentFromReader(bytes.NewReader(body))
	if err != nil {
		return nil, false
	}
	var found *topic
	doc.Find(`script[type="application/ld+json"]`).EachWithBreak(func(_ int, s *goquery.Selection) bool {
		var ld struct {
			Graph []json.RawMessage `json:"@graph"`
		}
		raw := s.Text()
		if err := json.Unmarshal([]byte(raw), &ld); err != nil {
			return true
		}
		nodes := ld.Graph
		if len(nodes) == 0 {
			nodes = []json.RawMessage{json.RawMessage(raw)}
		}
		for _, n := range nodes {
			var a struct {
				Type          string `json:"@type"`
				Headline      string `json:"headline"`
				ArticleBody   string `json:"articleBody"`
				DatePublished string `json:"datePublished"`
				Image         []struct {
					ContentURL string `json:"contentUrl"`
				} `json:"image"`
			}
			if json.Unmarshal(n, &a) != nil || a.Type != "Article" {
				continue
			}
			t := &topic{Headline: strings.TrimSpace(a.Headline), Body: strings.TrimSpace(a.ArticleBody)}
			if ts, err := time.Parse(time.RFC3339, a.DatePublished); err == nil {
				t.Published = ts
			}
			for _, im := range a.Image {
				if u := strings.TrimSpace(im.ContentURL); strings.HasPrefix(u, "https://") {
					t.Images = append(t.Images, u)
				}
			}
			found = t
			return false
		}
		return true
	})
	return found, found != nil
}

var moscow = time.FixedZone("MSK", 3*3600)

var ruMonths = map[string]time.Month{
	"янв": 1, "фев": 2, "мар": 3, "апр": 4, "мая": 5, "май": 5, "июн": 6,
	"июл": 7, "авг": 8, "сен": 9, "окт": 10, "ноя": 11, "дек": 12,
}

// parseWhen turns the <time> label of the group page into a UTC time;
// nil when the label is not understood.
func parseWhen(label string, now time.Time) *time.Time {
	now = now.In(moscow)
	fields := strings.Fields(strings.ToLower(label))
	if len(fields) == 0 {
		return nil
	}
	day := now
	switch {
	case fields[0] == "вчера":
		day = now.AddDate(0, 0, -1)
		fields = fields[1:]
	case fields[0] == "сегодня":
		fields = fields[1:]
	case len(fields) >= 2 && ruMonths[strings.TrimSuffix(fields[1], ".")] != 0:
		d, err := strconv.Atoi(fields[0])
		if err != nil || d < 1 || d > 31 {
			return nil
		}
		month := ruMonths[strings.TrimSuffix(fields[1], ".")]
		year := now.Year()
		fields = fields[2:]
		if len(fields) > 0 && len(fields[0]) == 4 {
			if y, err := strconv.Atoi(fields[0]); err == nil {
				year = y
			}
			fields = fields[1:]
		}
		day = time.Date(year, month, d, 0, 0, 0, 0, moscow)
		if day.After(now) {
			day = day.AddDate(-1, 0, 0)
		}
		if len(fields) == 0 {
			t := day.UTC()
			return &t
		}
	}
	if len(fields) != 1 {
		return nil
	}
	hm := strings.Split(fields[0], ":")
	if len(hm) != 2 {
		return nil
	}
	h, err1 := strconv.Atoi(hm[0])
	m, err2 := strconv.Atoi(hm[1])
	if err1 != nil || err2 != nil || h > 23 || m > 59 {
		return nil
	}
	t := time.Date(day.Year(), day.Month(), day.Day(), h, m, 0, 0, moscow).UTC()
	return &t
}
