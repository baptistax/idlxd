package models

type Highlight struct {
	ID    string
	Title string
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
	Provenance         string         `json:"-"`
	ID                 string         `json:"id"`
	PK                 string         `json:"pk"`
	Code               string         `json:"code"`
	TakenAt            int64          `json:"taken_at"`
	MediaType          int            `json:"media_type"`
	ProductType        string         `json:"product_type"`
	User               IGUser         `json:"user"`
	ImageVersions2     ImageVersions2 `json:"image_versions2"`
	VideoVersions      []Candidate    `json:"video_versions"`
	VideoDashManifest  string         `json:"video_dash_manifest"`
	CarouselMedia      []Media        `json:"carousel_media"`
	CarouselMediaCount *int           `json:"carousel_media_count"`
}

func (m Media) IsReel() bool { return m.ProductType == "clips" || m.ProductType == "reels" }
