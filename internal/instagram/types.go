package instagram

type Profile struct {
	Username string
	UserID   string
}

type Candidate struct {
	URL    string `json:"url"`
	Width  int    `json:"width"`
	Height int    `json:"height"`
	Type   int    `json:"type"`
}

type ImageVersions2 struct {
	Candidates []Candidate `json:"candidates"`
}

type IGUser struct {
	PK       string `json:"pk"`
	Username string `json:"username"`
}

type Media struct {
	ID                string         `json:"id"`
	PK                string         `json:"pk"`
	Code              string         `json:"code"`
	TakenAt           int64          `json:"taken_at"`
	MediaType         int            `json:"media_type"`
	ProductType       string         `json:"product_type"`
	User              IGUser         `json:"user"`
	ImageVersions2    ImageVersions2 `json:"image_versions2"`
	VideoVersions     []Candidate    `json:"video_versions"`
	VideoDashManifest string         `json:"video_dash_manifest"`
	CarouselMedia     []Media        `json:"carousel_media"`
}

type PageInfo struct {
	EndCursor   string `json:"end_cursor"`
	HasNextPage bool   `json:"has_next_page"`
}

type Highlight struct {
	ID    string
	Title string
}
