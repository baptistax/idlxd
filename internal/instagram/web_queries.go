package instagram

// Public persisted query identifiers name read-only web operations. They are
// version-specific schema metadata. Requests supply fresh target variables.
const (
	postsFirstDoc      = "30543471925243353"
	profileDoc         = "29609640381972919"
	highlightsFirstDoc = "29095961123359686"
	highlightsNextDoc  = "28496033073409588"
	postsNextDoc       = "29597370573235791"
	reelsFirstDoc      = "28280418711631396"
	reelsNextDoc       = "28237651492507044"
	storiesDoc         = "39989351314045574"
	trayDoc            = "26970053832668570"
)

// These persisted operations require explicit Booleans for Relay's field
// selections, even for UI fields the downloader doesn't consume.
func addWebQuerySelections(doc string, vars object) {
	set := func(value bool, names ...string) {
		for _, name := range names {
			vars["__relay_internal__pv__Polaris"+name+"relayprovider"] = value
		}
	}
	switch doc {
	case profileDoc:
		set(true, "CannesGuardianExperienceEnabled", "DSA16EntrypointEnabled", "ProfileInformTreatmentBannerEnabled")
		set(false, "CASB976ProfileEnabled", "ProfileRingsGradientEnabled", "RepostsConsumptionEnabled", "ShortDramaEnabled", "WebSchoolsEnabled")
	case postsFirstDoc, postsNextDoc:
		set(false, "MultiCaptionCarouselEnabled", "ProductTagIndicatorEnabled", "ReelsRecoDebugOverlayEnabled", "SEOIsCrawler", "ShortDramaEnabled", "ThreadsStyleCommentsEnabled")
	case reelsFirstDoc, reelsNextDoc:
		set(false, "ShortDramaEnabled")
	case storiesDoc, highlightsFirstDoc, highlightsNextDoc:
		set(true, "CommunityNoteStoriesLabelEnabled")
	}
}
