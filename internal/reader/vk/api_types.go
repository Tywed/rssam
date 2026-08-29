package vk

// API response types for newsfeed.search (extended=1).

type searchResponse struct {
	Response *searchResponseBody `json:"response"`
	Error    *apiError           `json:"error"`
}

type apiError struct {
	ErrorCode int    `json:"error_code"`
	ErrorMsg  string `json:"error_msg"`
}

type searchResponseBody struct {
	Items    []wallPost `json:"items"`
	Profiles []profile  `json:"profiles"`
	Groups   []group    `json:"groups"`
}

type wallPost struct {
	ID          int          `json:"id"`
	OwnerID     int          `json:"owner_id"`
	FromID      int          `json:"from_id"`
	Date        int          `json:"date"`
	Text        string       `json:"text"`
	Attachments []attachment `json:"attachments"`
	CopyHistory []wallPost   `json:"copy_history"`
}

type profile struct {
	ID        int    `json:"id"`
	FirstName string `json:"first_name"`
	LastName  string `json:"last_name"`
}

type group struct {
	ID   int    `json:"id"`
	Name string `json:"name"`
}

type attachment struct {
	Type  string       `json:"type"`
	Photo *photoAttach `json:"photo,omitempty"`
	Video *videoAttach `json:"video,omitempty"`
	Link  *linkAttach  `json:"link,omitempty"`
}

type photoAttach struct {
	ID    int         `json:"id"`
	Text  string      `json:"text"`
	Sizes []photoSize `json:"sizes"`
}

type photoSize struct {
	Type  string `json:"type"`
	URL   string `json:"url"`
	Width int    `json:"width"`
}

type videoAttach struct {
	ID          int    `json:"id"`
	OwnerID     int    `json:"owner_id"`
	Title       string `json:"title"`
	Description string `json:"description"`
}

type linkAttach struct {
	URL         string `json:"url"`
	Title       string `json:"title"`
	Description string `json:"description"`
}
