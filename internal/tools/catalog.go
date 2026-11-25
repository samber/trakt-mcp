package tools

// catalog lists the tool groups NewServer registers, in order. Each group
// is one function calling Register once per tool; a new group is one more
// line here, and the registry tests pick it up without changes.
var catalog = []func(*Server){
	registerMovieTools,
	registerMovieCatalogTools,
	registerPeopleTools,
	registerCommentTools,
	registerShowTools,
	registerSeasonTools,
	registerEpisodeTools,
	registerHistoryTools,
	registerWatchlistTools,
	registerCollectionTools,
	registerRatingTools,
	registerCheckinTools,
	registerRecommendationTools,
	registerSmartListTools,
	registerCalendarTools,
	registerWatchNowTools,
	registerSearchExtraTools,
	registerMovieExtraTools,
	registerShowExtraTools,
	registerListTools,
	registerListWriteTools,
	registerWatchlistEditTools,
	registerNoteTools,
	registerProgressTools,
	registerReferenceTools,
	registerFavoriteTools,
	registerUpNextTools,
}
