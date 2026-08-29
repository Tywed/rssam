package max

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"rssam/internal/model"
	"rssam/internal/storage"
)

// flexStringID accepts JSON strings or numbers (Max API may send numeric ids).
type flexStringID string

func (f *flexStringID) UnmarshalJSON(b []byte) error {
	s := strings.TrimSpace(string(b))
	if s == "" || s == "null" {
		*f = ""
		return nil
	}
	if s[0] == '"' {
		var str string
		if err := json.Unmarshal(b, &str); err != nil {
			return err
		}
		*f = flexStringID(strings.TrimSpace(str))
		return nil
	}
	var n json.Number
	if err := json.Unmarshal(b, &n); err != nil {
		return err
	}
	*f = flexStringID(n.String())
	return nil
}

func (f flexStringID) String() string {
	return string(f)
}

// ForwardedMessage is optional forwarded metadata from the API.
type ForwardedMessage struct {
	ChatName    string `json:"chat_name"`
	ChatLink    string `json:"chat_link"`
	MessageText string `json:"message_text"`
}

// APIMessage is a single message from the Max channel API.
type APIMessage struct {
	Time             int64             `json:"time"`
	Text             string            `json:"text"`
	MessageURL       string            `json:"message_url"`
	ID               flexStringID      `json:"id"`
	ForwardedMessage *ForwardedMessage `json:"forwarded_message,omitempty"`
}

// APIResponse is the JSON body from GET /channel/{name}/messages.
type APIResponse struct {
	Messages []APIMessage `json:"messages"`
}

// EntryFromMessage maps an API message to a storage entry.
func EntryFromMessage(channelName string, msg APIMessage) storage.CreateEntryParams {
	text := strings.TrimSpace(msg.Text)
	title := TitleFromText(text, 140)
	content := BuildContentHTML(text, msg.ForwardedMessage)

	entryURL := strings.TrimSpace(msg.MessageURL)
	id := msg.ID.String()
	if entryURL == "" && id != "" {
		entryURL = fmt.Sprintf("https://max.ru/%s/%s", channelName, id)
	}
	entryURL = model.NormalizeURL(entryURL)

	var pub *time.Time
	if msg.Time > 0 {
		t := time.UnixMilli(msg.Time).UTC()
		pub = &t
	}

	author := channelName
	hash := model.DedupHashFromURL(entryURL)
	if hash == "" && id != "" {
		hash = model.DedupHashFromURL(channelName + "/" + id)
	}

	return storage.CreateEntryParams{
		Title:       title,
		URL:         entryURL,
		Content:     content,
		Author:      &author,
		PublishedAt: pub,
		Hash:        hash,
		Status:      storage.EntryStatusUnread,
	}
}
