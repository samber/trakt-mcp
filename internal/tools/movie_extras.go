package tools

import (
	"context"
	"net/http"

	"github.com/samber/trakt-mcp/internal/trakt"
	"github.com/samber/trakt-mcp/internal/trakt/api"
)

const (
	// maxCountryFieldRunes bounds a country code echoed from Trakt: ISO 3166-1
	// codes have 2 letters, the margin tolerates a short variant.
	maxCountryFieldRunes = 8
	// maxCertificationRunes bounds a certification such as "PG-13" or "TV-MA".
	maxCertificationRunes = 32
	// maxReleaseTypeRunes bounds a release type: the longest documented value
	// (theatrical) has 10 characters, the margin tolerates a new value.
	maxReleaseTypeRunes = 32
	// maxReleaseDateRunes bounds a release date: YYYY-MM-DD has 10 characters,
	// the margin tolerates a timestamp.
	maxReleaseDateRunes = 32
	// maxReleaseNoteRunes bounds the free-text note of a release, which comes
	// from community edits.
	maxReleaseNoteRunes = 200
	// maxSlugRunes bounds a slug echoed from Trakt.
	maxSlugRunes = 200
)

// MovieTranslationsArgs are the arguments of the movie handler of list_translations.
type MovieTranslationsArgs struct {
	MovieRef
	Language string `json:"language,omitempty" jsonschema:"two-letter ISO 639-1 code such as fr; omit it for every language"`
}

// MovieListsArgs are the arguments of the movie handler of list_item_lists.
type MovieListsArgs struct {
	PageArgs
	MovieRef
	ListType string `json:"list_type,omitempty" jsonschema:"all, personal, official, watchlist or favorites (default all)"`
	Sort     string `json:"sort,omitempty"      jsonschema:"popular, likes, comments, items, added or updated (default popular)"`
}

// MovieReleasesArgs are the arguments of fetch_movie_releases.
type MovieReleasesArgs struct {
	MovieRef
	Country string `json:"country" jsonschema:"two-letter lower-case ISO 3166-1 country code such as fr or us"`
}

// Alias is an alternate title of a title, in a country.
type Alias struct {
	Title   string `json:"title"`
	Country string `json:"country,omitempty" jsonschema:"country the title is used in"`
}

type traktAlias struct {
	Title   string  `json:"title"`
	Country *string `json:"country"`
}

func trimAlias(a traktAlias) Alias {
	return Alias{Title: boundText(a.Title, maxTitleRunes), Country: boundText(deref(a.Country), maxCountryFieldRunes)}
}

// StudioIDs are the identifiers of a production studio.
type StudioIDs struct {
	Trakt int    `json:"trakt"`
	Slug  string `json:"slug"`
	TMDB  int    `json:"tmdb,omitempty"`
}

// Studio is a production studio of a title.
type Studio struct {
	Name    string    `json:"name"`
	Country string    `json:"country,omitempty"`
	IDs     StudioIDs `json:"ids"`
}

type traktStudio struct {
	Name    string  `json:"name"`
	Country *string `json:"country"`
	IDs     struct {
		Trakt int     `json:"trakt"`
		Slug  *string `json:"slug"`
		TMDB  *int    `json:"tmdb"`
	} `json:"ids"`
}

func trimStudio(s traktStudio) Studio {
	return Studio{
		Name:    boundText(s.Name, maxTitleRunes),
		Country: boundText(deref(s.Country), maxCountryFieldRunes),
		IDs:     StudioIDs{Trakt: s.IDs.Trakt, Slug: boundText(deref(s.IDs.Slug), maxSlugRunes), TMDB: deref(s.IDs.TMDB)},
	}
}

// Release is one release of a movie in a country.
type Release struct {
	Country       string `json:"country"`
	Certification string `json:"certification,omitempty"`
	ReleaseDate   string `json:"release_date" jsonschema:"YYYY-MM-DD"`
	ReleaseType   string `json:"release_type,omitempty" jsonschema:"unknown, premiere, limited, theatrical, digital, physical or tv"`
	Note          string `json:"note,omitempty"`
}

type traktRelease struct {
	Country       string  `json:"country"`
	Certification *string `json:"certification"`
	ReleaseDate   string  `json:"release_date"`
	ReleaseType   *string `json:"release_type"`
	Note          *string `json:"note"`
}

func trimRelease(r traktRelease) Release {
	return Release{
		Country:       boundText(r.Country, maxCountryFieldRunes),
		Certification: boundText(deref(r.Certification), maxCertificationRunes),
		ReleaseDate:   boundText(r.ReleaseDate, maxReleaseDateRunes),
		ReleaseType:   boundText(deref(r.ReleaseType), maxReleaseTypeRunes),
		Note:          boundText(deref(r.Note), maxReleaseNoteRunes),
	}
}

func registerMovieExtraTools(s *Server) {
	Register(s, Spec{
		Name: "fetch_movie_releases", Title: "Fetch movie releases", Access: Read,
		Description: "List a movie's releases in a country: date, type (unknown, premiere, limited, theatrical, digital, physical, tv), certification and note." + untrustedNote,
	}, fetchMovieReleases)
}

func fetchMovieStats(ctx context.Context, c *trakt.UserClient, in MovieRef) (Stats, error) {
	id, err := in.path()
	if err != nil {
		return Stats{}, err
	}

	return fetchParsed(func() (*http.Response, error) {
		return c.API().GetMoviesStats(ctx, id)
	}, statsOf)
}

func fetchMovieWatching(ctx context.Context, c *trakt.UserClient, in MovieRef) (Watching, error) {
	id, err := in.path()
	if err != nil {
		return Watching{}, err
	}

	return fetchParsed(func() (*http.Response, error) {
		return c.API().GetMoviesWatching(ctx, id, &api.GetMoviesWatchingParams{})
	}, watchingOf)
}

func fetchMovieTranslations(ctx context.Context, c *trakt.UserClient, in MovieTranslationsArgs) (Listing[Translation], error) {
	id, err := in.path()
	if err != nil {
		return Listing[Translation]{}, err
	}

	lang, err := languageArg(in.Language)
	if err != nil {
		return Listing[Translation]{}, err
	}

	return fetchParsed(func() (*http.Response, error) {
		return c.API().GetMoviesTranslations(ctx, id, &api.GetMoviesTranslationsParams{Language: lang})
	}, parseTranslations)
}

func fetchMovieAliases(ctx context.Context, c *trakt.UserClient, in MovieRef) (Listing[Alias], error) {
	id, err := in.path()
	if err != nil {
		return Listing[Alias]{}, err
	}

	return fetchParsed(func() (*http.Response, error) {
		return c.API().GetMoviesAliases(ctx, id)
	}, func(body []byte) (Listing[Alias], error) { return unpaged(body, trimAlias) })
}

func fetchMovieStudios(ctx context.Context, c *trakt.UserClient, in MovieRef) (Listing[Studio], error) {
	id, err := in.path()
	if err != nil {
		return Listing[Studio]{}, err
	}

	return fetchParsed(func() (*http.Response, error) {
		return c.API().GetMoviesStudios(ctx, id)
	}, func(body []byte) (Listing[Studio], error) { return unpaged(body, trimStudio) })
}

func fetchMovieLists(ctx context.Context, c *trakt.UserClient, in MovieListsArgs) (Listing[TraktList], error) {
	id, err := in.path()
	if err != nil {
		return Listing[TraktList]{}, err
	}

	listType, sortBy, err := listArgs(in.ListType, in.Sort)
	if err != nil {
		return Listing[TraktList]{}, err
	}

	return paginateDecoded(ctx, in.PageArgs, trimList, func(page, limit int) (*http.Response, error) {
		return c.API().GetMoviesLists(ctx, id, api.GetMoviesListsParamsType(listType), api.GetMoviesListsParamsSort(sortBy),
			&api.GetMoviesListsParams{Page: &page, Limit: &limit})
	})
}

func fetchMovieReleases(ctx context.Context, c *trakt.UserClient, in MovieReleasesArgs) (Listing[Release], error) {
	id, err := in.path()
	if err != nil {
		return Listing[Release]{}, err
	}

	country, err := countryArg(in.Country)
	if err != nil {
		return Listing[Release]{}, err
	}

	return fetchParsed(func() (*http.Response, error) {
		return c.API().GetMoviesReleases(ctx, id, country)
	}, func(body []byte) (Listing[Release], error) { return unpaged(body, trimRelease) })
}
