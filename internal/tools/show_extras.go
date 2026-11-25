package tools

import (
	"bytes"
	"context"
	"net/http"

	"github.com/samber/trakt-mcp/internal/trakt"
	"github.com/samber/trakt-mcp/internal/trakt/api"
)

// ShowTranslationsArgs are the arguments of the show handler of list_translations.
type ShowTranslationsArgs struct {
	ShowRef
	Language string `json:"language,omitempty" jsonschema:"two-letter ISO 639-1 code such as fr; omit it for every language"`
}

// ShowListsArgs are the arguments of the show handler of list_item_lists.
type ShowListsArgs struct {
	PageArgs
	ShowRef
	ListType string `json:"list_type,omitempty" jsonschema:"all, personal, official, watchlist or favorites (default all)"`
	Sort     string `json:"sort,omitempty"      jsonschema:"popular, likes, comments, items, added or updated (default popular)"`
}

// ShowCertification is the content certification of a TV show in a country.
type ShowCertification struct {
	Country       string `json:"country"`
	Certification string `json:"certification" jsonschema:"for example TV-MA or 18"`
}

type traktShowCertification struct {
	Country       string `json:"country"`
	Certification string `json:"certification"`
}

func trimShowCertification(c traktShowCertification) ShowCertification {
	return ShowCertification{
		Country:       boundText(c.Country, maxCountryFieldRunes),
		Certification: boundText(c.Certification, maxCertificationRunes),
	}
}

// MaybeEpisode is an episode, or an empty object when Trakt knows none. Every
// field is optional in the output schema so that the empty object validates;
// season and number are pointers because 0 is a real season (the specials).
type MaybeEpisode struct {
	Season      *int        `json:"season,omitempty"`
	Number      *int        `json:"number,omitempty"`
	Title       string      `json:"title,omitempty"`
	Overview    string      `json:"overview,omitempty"`
	Rating      float64     `json:"rating,omitempty"`
	Votes       int         `json:"votes,omitempty"`
	Runtime     int         `json:"runtime,omitempty"      jsonschema:"minutes"`
	FirstAired  string      `json:"first_aired,omitempty"`
	EpisodeType string      `json:"episode_type,omitempty" jsonschema:"for example standard, series_premiere, season_finale"`
	IDs         *EpisodeIDs `json:"ids,omitempty"`
}

func (MaybeEpisode) resultCount() int { return 1 }

// parseMaybeEpisode reads the body of the next and last episode endpoints.
// Trakt answers 204 with no body when the show has no such episode; a 200
// whose body is null, {} or an episode without a Trakt id means the same.
func parseMaybeEpisode(body []byte) (MaybeEpisode, error) {
	if len(bytes.TrimSpace(body)) == 0 {
		return MaybeEpisode{}, nil
	}

	ep, err := parseEpisode(body)
	if err != nil {
		return MaybeEpisode{}, err
	}

	if ep.IDs.Trakt == 0 {
		return MaybeEpisode{}, nil
	}

	return MaybeEpisode{
		Season: &ep.Season, Number: &ep.Number, Title: ep.Title, Overview: ep.Overview, Rating: ep.Rating, Votes: ep.Votes,
		Runtime: ep.Runtime, FirstAired: ep.FirstAired, EpisodeType: ep.EpisodeType, IDs: &ep.IDs,
	}, nil
}

func registerShowExtraTools(s *Server) {
	Register(s, Spec{
		Name: "get_next_episode", Title: "Get TV show next episode to air", Access: Read,
		Description: "Get the next episode of a TV show to air on TV, with its air date. Returns an empty object when none is scheduled. " +
			"For the next episode the user has to watch, use get_show_progress or list_up_next.",
	}, fetchShowNextEpisode)
	Register(s, Spec{
		Name: "get_last_episode", Title: "Get TV show last aired episode", Access: Read,
		Description: "Get the most recently aired episode of a TV show. Returns an empty object when none has aired.",
	}, fetchShowLastEpisode)
	Register(s, Spec{
		Name: "list_show_certifications", Title: "List TV show certifications", Access: Read,
		Description: "List a TV show's content certifications (such as TV-MA) with the country of each.",
	}, fetchShowCertifications)
}

func fetchShowStats(ctx context.Context, c *trakt.UserClient, in ShowRef) (Stats, error) {
	id, err := in.path()
	if err != nil {
		return Stats{}, err
	}

	return fetchParsed(func() (*http.Response, error) {
		return c.API().GetShowsStats(ctx, id)
	}, statsOf)
}

func fetchShowWatching(ctx context.Context, c *trakt.UserClient, in ShowRef) (Watching, error) {
	id, err := in.path()
	if err != nil {
		return Watching{}, err
	}

	return fetchParsed(func() (*http.Response, error) {
		return c.API().GetShowsWatching(ctx, id, &api.GetShowsWatchingParams{})
	}, watchingOf)
}

func fetchShowTranslations(ctx context.Context, c *trakt.UserClient, in ShowTranslationsArgs) (Listing[Translation], error) {
	id, err := in.path()
	if err != nil {
		return Listing[Translation]{}, err
	}

	lang, err := languageArg(in.Language)
	if err != nil {
		return Listing[Translation]{}, err
	}

	return fetchParsed(func() (*http.Response, error) {
		return c.API().GetShowsTranslations(ctx, id, &api.GetShowsTranslationsParams{Language: lang})
	}, parseTranslations)
}

func fetchShowAliases(ctx context.Context, c *trakt.UserClient, in ShowRef) (Listing[Alias], error) {
	id, err := in.path()
	if err != nil {
		return Listing[Alias]{}, err
	}

	return fetchParsed(func() (*http.Response, error) {
		return c.API().GetShowsAliases(ctx, id)
	}, func(body []byte) (Listing[Alias], error) { return unpaged(body, trimAlias) })
}

func fetchShowStudios(ctx context.Context, c *trakt.UserClient, in ShowRef) (Listing[Studio], error) {
	id, err := in.path()
	if err != nil {
		return Listing[Studio]{}, err
	}

	return fetchParsed(func() (*http.Response, error) {
		return c.API().GetShowsStudios(ctx, id)
	}, func(body []byte) (Listing[Studio], error) { return unpaged(body, trimStudio) })
}

func fetchShowLists(ctx context.Context, c *trakt.UserClient, in ShowListsArgs) (Listing[TraktList], error) {
	id, err := in.path()
	if err != nil {
		return Listing[TraktList]{}, err
	}

	return listsContaining(ctx, in.PageArgs, in.ListType, in.Sort, func(listType, sortBy string, page, limit int) (*http.Response, error) {
		return c.API().GetShowsLists(ctx, id, api.GetShowsListsParamsType(listType), api.GetShowsListsParamsSort(sortBy),
			&api.GetShowsListsParams{Page: &page, Limit: &limit})
	})
}

// listsContaining validates the list type and sort, then pages through the
// lists that hold a title.
func listsContaining(ctx context.Context, pages PageArgs, listTypeArg, sortArg string, call func(listType, sortBy string, page, limit int) (*http.Response, error)) (Listing[TraktList], error) {
	listType, sortBy, err := listArgs(listTypeArg, sortArg)
	if err != nil {
		return Listing[TraktList]{}, err
	}

	return paginateDecoded(ctx, pages, trimList, func(page, limit int) (*http.Response, error) {
		return call(listType, sortBy, page, limit)
	})
}

func fetchShowNextEpisode(ctx context.Context, c *trakt.UserClient, in ShowRef) (MaybeEpisode, error) {
	id, err := in.path()
	if err != nil {
		return MaybeEpisode{}, err
	}

	return fetchParsed(func() (*http.Response, error) {
		return c.API().GetShowsNextEpisode(ctx, id, &api.GetShowsNextEpisodeParams{Extended: extendedFullPtr()})
	}, parseMaybeEpisode)
}

func fetchShowLastEpisode(ctx context.Context, c *trakt.UserClient, in ShowRef) (MaybeEpisode, error) {
	id, err := in.path()
	if err != nil {
		return MaybeEpisode{}, err
	}

	return fetchParsed(func() (*http.Response, error) {
		return c.API().GetShowsLastEpisode(ctx, id, &api.GetShowsLastEpisodeParams{Extended: extendedFullPtr()})
	}, parseMaybeEpisode)
}

func fetchShowCertifications(ctx context.Context, c *trakt.UserClient, in ShowRef) (Listing[ShowCertification], error) {
	id, err := in.path()
	if err != nil {
		return Listing[ShowCertification]{}, err
	}

	return fetchParsed(func() (*http.Response, error) {
		return c.API().GetShowsCertifications(ctx, id)
	}, func(body []byte) (Listing[ShowCertification], error) { return unpaged(body, trimShowCertification) })
}
