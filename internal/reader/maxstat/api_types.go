package maxstat

type postsResponse struct {
	Total int    `json:"total"`
	Posts []post `json:"posts"`
}

type post struct {
	ID           string         `json:"id"`
	ChannelID    string         `json:"channel_id"`
	Type         string         `json:"type"`
	URL          string         `json:"url"`
	Text         string         `json:"text"`
	Views        int            `json:"views"`
	Likes        int            `json:"likes"`
	LikesDetail  map[string]int `json:"likes_detailed"`
	Attachments  []attachment   `json:"attachments"`
	PublishedAt  string         `json:"published_at"`
}

type attachment struct {
	Type string `json:"attachment_type"`
	URL  string `json:"attachment_url"`
}

type apiError struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}
