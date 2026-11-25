package tools

import (
	"context"
	"net/http"

	"github.com/samber/trakt-mcp/internal/trakt"
	"github.com/samber/trakt-mcp/internal/trakt/api"
)

const (
	// minEpisode is 1: Trakt numbers episodes from 1. maxEpisode is far above
	// any real season.
	minEpisode = 1
	maxEpisode = 9999
)

func checkEpisode(n int) error {
	if n < minEpisode || n > maxEpisode {
		return invalidArgument(fieldEpisode)
	}

	return nil
}

// EpisodeRef names an episode of a show.
type EpisodeRef struct {
	SeasonRef
	Episode int `json:"episode" jsonschema:"episode number in the season, 1 to 9999"`
}

func (r EpisodeRef) path() (string, int, int, error) {
	id, season, err := r.SeasonRef.path()
	if err != nil {
		return "", 0, 0, err
	}

	return id, season, r.Episode, checkEpisode(r.Episode)
}

func parseEpisode(body []byte) (Episode, error) {
	raw, err := decodeAs[traktEpisode](body)

	return raw.trim(maxOverviewRunes), err
}

// EpisodeTranslationsArgs are the arguments of fetch_episode_translations.
type EpisodeTranslationsArgs struct {
	EpisodeRef
	Language string `json:"language,omitempty" jsonschema:"two-letter ISO 639-1 code such as fr; omit it for every language"`
}

// EpisodeListsArgs are the arguments of fetch_episode_lists.
type EpisodeListsArgs struct {
	PageArgs
	EpisodeRef
	ListType string `json:"list_type,omitempty" jsonschema:"all, personal, official, watchlist or favorites (default all)"`
	Sort     string `json:"sort,omitempty"      jsonschema:"popular, likes, comments, items, added or updated (default popular)"`
}

func registerEpisodeTools(s *Server) {
	Register(s, Spec{
		Name: "fetch_episode_ratings", Title: "Fetch episode ratings", Access: Read,
		Description: "Get a TV show episode's average rating, vote count and the number of votes for each rating from 1 to 10.",
	}, fetchEpisodeRatings)
	Register(s, Spec{
		Name: "fetch_episode_stats", Title: "Fetch episode stats", Access: Read,
		Description: "Get a TV show episode's counters: watchers, plays, collectors, comments, lists and votes.",
	}, fetchEpisodeStats)
	Register(s, Spec{
		Name: "fetch_episode_people", Title: "Fetch episode people", Access: Read,
		Description: "Get a TV show episode's cast (name, character) and crew grouped by department. Long credit lists are cut.",
	}, fetchEpisodePeople)
	Register(s, Spec{
		Name: "fetch_episode_videos", Title: "Fetch episode videos", Access: Read,
		Description: "List a TV show episode's trailers and other videos with title, type, site, url, publication date and a ready markdown link.",
	}, fetchEpisodeVideos)
	Register(s, Spec{
		Name: "fetch_episode_watching", Title: "Fetch episode watchers", Access: Read,
		Description: "Count the users watching a TV show episode right now and list their usernames, up to 50. Private accounts are counted, not listed.",
	}, fetchEpisodeWatching)
	Register(s, Spec{
		Name: "fetch_episode_translations", Title: "Fetch episode translations", Access: Read,
		Description: "Get a TV show episode's title and overview in one language (two-letter code) or in every language.",
	}, fetchEpisodeTranslations)
	Register(s, Spec{
		Name: "fetch_episode_lists", Title: "Fetch lists with an episode", Access: Read,
		Description: "List the Trakt lists that contain a TV show episode, by list type and sort. Takes limit and page." + untrustedNote,
	}, fetchEpisodeLists)
}

func fetchEpisodeSummary(ctx context.Context, c *trakt.UserClient, in EpisodeRef) (Episode, error) {
	id, season, episode, err := in.path()
	if err != nil {
		return Episode{}, err
	}

	return fetchParsed(func() (*http.Response, error) {
		return c.API().GetShowsEpisodeSummary(ctx, id, season, episode, &api.GetShowsEpisodeSummaryParams{Extended: extendedFullPtr()})
	}, parseEpisode)
}

func fetchEpisodeRatings(ctx context.Context, c *trakt.UserClient, in EpisodeRef) (Rating, error) {
	id, season, episode, err := in.path()
	if err != nil {
		return Rating{}, err
	}

	return fetchParsed(func() (*http.Response, error) {
		return c.API().GetShowsEpisodeRatings(ctx, id, season, episode, &api.GetShowsEpisodeRatingsParams{})
	}, ratingOf)
}

func fetchEpisodeStats(ctx context.Context, c *trakt.UserClient, in EpisodeRef) (Stats, error) {
	id, season, episode, err := in.path()
	if err != nil {
		return Stats{}, err
	}

	return fetchParsed(func() (*http.Response, error) {
		return c.API().GetShowsEpisodeStats(ctx, id, season, episode)
	}, statsOf)
}

func fetchEpisodePeople(ctx context.Context, c *trakt.UserClient, in EpisodeRef) (People, error) {
	id, season, episode, err := in.path()
	if err != nil {
		return People{}, err
	}

	return fetchParsed(func() (*http.Response, error) {
		return c.API().GetShowsEpisodePeople(ctx, id, season, episode, &api.GetShowsEpisodePeopleParams{})
	}, parsePeople)
}

func fetchEpisodeVideos(ctx context.Context, c *trakt.UserClient, in EpisodeRef) (Listing[Video], error) {
	id, season, episode, err := in.path()
	if err != nil {
		return Listing[Video]{}, err
	}

	return fetchParsed(func() (*http.Response, error) {
		return c.API().GetShowsEpisodeVideos(ctx, id, season, episode)
	}, parseVideos)
}

func fetchEpisodeWatching(ctx context.Context, c *trakt.UserClient, in EpisodeRef) (Watching, error) {
	id, season, episode, err := in.path()
	if err != nil {
		return Watching{}, err
	}

	return fetchParsed(func() (*http.Response, error) {
		return c.API().GetShowsEpisodeWatching(ctx, id, season, episode, &api.GetShowsEpisodeWatchingParams{})
	}, watchingOf)
}

func fetchEpisodeTranslations(ctx context.Context, c *trakt.UserClient, in EpisodeTranslationsArgs) (Listing[Translation], error) {
	id, season, episode, err := in.path()
	if err != nil {
		return Listing[Translation]{}, err
	}

	lang, err := languageArg(in.Language)
	if err != nil {
		return Listing[Translation]{}, err
	}

	return fetchParsed(func() (*http.Response, error) {
		return c.API().GetShowsEpisodeTranslations(ctx, id, season, episode, &api.GetShowsEpisodeTranslationsParams{Language: lang})
	}, parseTranslations)
}

func fetchEpisodeLists(ctx context.Context, c *trakt.UserClient, in EpisodeListsArgs) (Listing[TraktList], error) {
	id, season, episode, err := in.path()
	if err != nil {
		return Listing[TraktList]{}, err
	}

	listType, sortBy, err := listArgs(in.ListType, in.Sort)
	if err != nil {
		return Listing[TraktList]{}, err
	}

	return paginateDecoded(ctx, in.PageArgs, trimList, func(page, limit int) (*http.Response, error) {
		return c.API().GetShowsEpisodeLists(ctx, id, season, episode, api.GetShowsEpisodeListsParamsType(listType), api.GetShowsEpisodeListsParamsSort(sortBy),
			&api.GetShowsEpisodeListsParams{Page: &page, Limit: &limit})
	})
}
