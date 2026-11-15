package telegram

import (
	"strings"
	"testing"

	"github.com/PuerkitoBio/goquery"
)

func TestParse_AttributeValuesAreHTMLEscaped(t *testing.T) {
	page := `<div class="tgme_widget_message" data-post="c/1">
<a class="tgme_widget_message_photo_wrap" href="https://t.me/c/1?x=1&amp;y=&quot;" style="background-image:url('https://cdn/img.jpg?a=1&amp;b=&quot;')"></a>
<div class="tgme_widget_message_text">hi</div>
</div>`
	doc, err := goquery.NewDocumentFromReader(strings.NewReader(page))
	if err != nil {
		t.Fatal(err)
	}
	wrap := doc.Find("a.tgme_widget_message_photo_wrap").First()
	href, _ := wrap.Attr("href")
	if !strings.Contains(href, `"`) {
		t.Fatalf("test page must yield a raw quote in href, got %q", href)
	}
	out, _, _ := processPhoto(doc.Find("div.tgme_widget_message").First(), "c")
	if strings.Contains(out, `?x=1&y="`) || !strings.Contains(out, `&amp;y=&#34;`) {
		t.Fatalf("attribute not escaped: %s", out)
	}
}
