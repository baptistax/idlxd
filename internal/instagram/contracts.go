package instagram

// Provider-specific fields and known web relationships.
const (
	graphBase            = "https://graph.facebook.com"
	GraphVersion         = "v27.0"
	profileFields        = "id,username,media_count"
	mediaFields          = "id,media_type,media_product_type,media_url,timestamp,owner,username,shortcode"
	postsConnection      = "xdt_api__v1__feed__user_timeline_graphql_connection"
	reelsConnection      = "xdt_api__v1__clips__user__connection_v2"
	highlightsConnection = "xdt_api__v1__feed__reels_media__connection"
	storiesContainer     = "xdt_api__v1__feed__reels_media"
	webVersion           = "instagram-web"
)
