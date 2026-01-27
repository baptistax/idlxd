package models

type MediaType string

const (
	MediaTypePhoto MediaType = "photo"
	MediaTypeVideo MediaType = "video"
)

type MediaItem struct {
	ID        string
	Shortcode string
	Type      MediaType
	URL       string
	CreatedAt int64
}

type Highlight struct {
	ID    string
	Title string
}
