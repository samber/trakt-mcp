package tools

import (
	"context"
	"net/http"
	"regexp"
	"slices"
	"strings"

	"github.com/samber/trakt-mcp/internal/trakt"
	"github.com/samber/trakt-mcp/internal/trakt/api"
)

// fieldCountry names the country argument in tool errors.
const fieldCountry = "country"

// countryPattern is an ISO 3166-1 alpha-2 code in lower case, the form
// Trakt's watchnow paths take; it rejects "/", "." and anything else that
// could change the request path.
const countryPattern = `^[a-z]{2}$`

var countryRE = regexp.MustCompile(countryPattern)

// countryArg validates a country code.
func countryArg(s string) (string, error) {
	if !countryRE.MatchString(s) {
		return "", invalidArgument(fieldCountry)
	}

	return s, nil
}

// WatchNowArgs are the arguments shared by the where-to-watch tools.
type WatchNowArgs struct {
	Country string `json:"country" jsonschema:"two-letter lower-case ISO 3166-1 country code such as fr or us"`
}

// MovieWatchNowArgs are the arguments of fetch_movie_watchnow.
type MovieWatchNowArgs struct {
	MovieRef
	WatchNowArgs
}

// ShowWatchNowArgs are the arguments of fetch_show_watchnow.
type ShowWatchNowArgs struct {
	ShowRef
	WatchNowArgs
}

// EpisodeWatchNowArgs are the arguments of fetch_episode_watchnow.
type EpisodeWatchNowArgs struct {
	EpisodeRef
	WatchNowArgs
}

// WatchLink is one place to watch a title.
type WatchLink struct {
	Source    string `json:"source" jsonschema:"provider key such as netflix or apple_tv"`
	Link      string `json:"link,omitempty" jsonschema:"http or https URL of the title at the provider"`
	UHD       bool   `json:"uhd" jsonschema:"true when the title is offered in 4K"`
	Currency  string `json:"currency,omitempty"`
	RentPrice string `json:"rent_price,omitempty"`
	BuyPrice  string `json:"buy_price,omitempty"`
}

// WatchNow lists where a title can be watched in one country, by kind of
// offer: free, subscription, purchase (rent or buy), cable and cinema.
type WatchNow struct {
	Country      string      `json:"country"`
	Free         []WatchLink `json:"free"`
	Subscription []WatchLink `json:"subscription"`
	Purchase     []WatchLink `json:"purchase"`
	Cable        []WatchLink `json:"cable"`
	Cinema       []WatchLink `json:"cinema"`
}

func (WatchNow) resultCount() int { return 1 }

type traktWatchLink struct {
	Source   string  `json:"source"`
	Link     string  `json:"link"`
	UHD      bool    `json:"uhd"`
	Currency *string `json:"currency"`
	Prices   struct {
		Purchase *string `json:"purchase"`
		Rent     *string `json:"rent"`
	} `json:"prices"`
}

type traktWatchGroups struct {
	Free         []traktWatchLink `json:"free"`
	Subscription []traktWatchLink `json:"subscription"`
	Purchase     []traktWatchLink `json:"purchase"`
	Cable        []traktWatchLink `json:"cable"`
	Cinema       []traktWatchLink `json:"cinema"`
}

func trimWatchLink(l traktWatchLink) WatchLink {
	return WatchLink{
		Source:    boundText(l.Source, maxTitleRunes),
		Link:      httpURLOrEmpty(l.Link),
		UHD:       l.UHD,
		Currency:  deref(l.Currency),
		RentPrice: deref(l.Prices.Rent),
		BuyPrice:  deref(l.Prices.Purchase),
	}
}

// trimWatchLinks never returns nil, so the output holds no JSON null.
func trimWatchLinks(in []traktWatchLink) []WatchLink {
	out := make([]WatchLink, 0, len(in))
	for _, l := range in {
		out = append(out, trimWatchLink(l))
	}

	return out
}

// parseWatchNow reads Trakt's object keyed by country and keeps the group of
// the requested one.
func parseWatchNow(country string) func([]byte) (WatchNow, error) {
	return func(body []byte) (WatchNow, error) {
		byCountry, err := decodeAs[map[string]traktWatchGroups](body)
		if err != nil {
			return WatchNow{}, err
		}

		var groups traktWatchGroups

		for key, g := range byCountry {
			if strings.EqualFold(key, country) {
				groups = g
			}
		}

		return WatchNow{
			Country:      country,
			Free:         trimWatchLinks(groups.Free),
			Subscription: trimWatchLinks(groups.Subscription),
			Purchase:     trimWatchLinks(groups.Purchase),
			Cable:        trimWatchLinks(groups.Cable),
			Cinema:       trimWatchLinks(groups.Cinema),
		}, nil
	}
}

// WatchSource is a streaming provider Trakt knows.
type WatchSource struct {
	Group     string `json:"group" jsonschema:"key Trakt files the provider under, usually a country code"`
	Source    string `json:"source" jsonschema:"provider key to match against the source of a where-to-watch result"`
	Name      string `json:"name"`
	Type      string `json:"type,omitempty" jsonschema:"free, tv_everywhere, subscription, purchase or cinema"`
	Free      bool   `json:"free"`
	Cinema    bool   `json:"cinema"`
	Amazon    bool   `json:"amazon"`
	LinkCount int    `json:"link_count" jsonschema:"number of titles Trakt links to this provider"`
}

type traktWatchSource struct {
	Source    string  `json:"source"`
	Name      string  `json:"name"`
	Type      *string `json:"type"`
	Free      bool    `json:"free"`
	Cinema    bool    `json:"cinema"`
	Amazon    bool    `json:"amazon"`
	LinkCount int     `json:"link_count"`
}

// parseWatchSources flattens Trakt's list of group-keyed maps, groups in
// alphabetical order so the output is stable.
func parseWatchSources(body []byte) (Listing[WatchSource], error) {
	raw, err := decodeAs[[]map[string][]traktWatchSource](body)
	if err != nil {
		return Listing[WatchSource]{}, err
	}

	items := []WatchSource{}

	for _, byGroup := range raw {
		groups := make([]string, 0, len(byGroup))
		for group := range byGroup {
			groups = append(groups, group)
		}

		slices.Sort(groups)

		for _, group := range groups {
			for _, s := range byGroup[group] {
				items = append(items, WatchSource{
					Group: boundText(group, maxTitleRunes), Source: boundText(s.Source, maxTitleRunes), Name: boundText(s.Name, maxTitleRunes),
					Type: deref(s.Type), Free: s.Free, Cinema: s.Cinema, Amazon: s.Amazon, LinkCount: s.LinkCount,
				})
			}
		}
	}

	return Listing[WatchSource]{Items: items}, nil
}

// WatchSourcesArgs are the arguments of fetch_watchnow_sources: none.
type WatchSourcesArgs struct{}

func registerWatchNowTools(s *Server) {
	Register(s, Spec{
		Name: "fetch_movie_watchnow", Title: "Fetch where to watch a movie", Access: Read,
		Description: "List where a movie can be watched in a country: free, subscription, purchase (rent or buy), cable and cinema offers, each with provider and link.",
	}, fetchMovieWatchNow)
	Register(s, Spec{
		Name: "fetch_show_watchnow", Title: "Fetch where to watch a TV show", Access: Read,
		Description: "List where a TV series can be watched in a country: free, subscription, purchase (rent or buy), cable and cinema offers, each with provider and link.",
	}, fetchShowWatchNow)
	Register(s, Spec{
		Name: "fetch_episode_watchnow", Title: "Fetch where to watch a TV episode", Access: Read,
		Description: "List where one episode of a TV series can be watched in a country: free, subscription, purchase (rent or buy), cable and cinema offers, each with provider and link. Season 0 is the specials.",
	}, fetchEpisodeWatchNow)
	Register(s, Spec{
		Name: "fetch_watchnow_sources", Title: "Fetch streaming providers", Access: Read,
		Description: "List every streaming provider Trakt knows, with provider key, name, offer type and the number of titles linked. Use it to read the provider keys of where-to-watch results.",
	}, fetchWatchNowSources)
}

func watchNow(country string, call func(country string) (*http.Response, error)) (WatchNow, error) {
	country, err := countryArg(country)
	if err != nil {
		return WatchNow{}, err
	}

	return fetchParsed(func() (*http.Response, error) { return call(country) }, parseWatchNow(country))
}

func fetchMovieWatchNow(ctx context.Context, c *trakt.UserClient, in MovieWatchNowArgs) (WatchNow, error) {
	id, err := in.path()
	if err != nil {
		return WatchNow{}, err
	}

	return watchNow(in.Country, func(country string) (*http.Response, error) {
		return c.API().GetMoviesWatchnow(ctx, id, country, &api.GetMoviesWatchnowParams{})
	})
}

func fetchShowWatchNow(ctx context.Context, c *trakt.UserClient, in ShowWatchNowArgs) (WatchNow, error) {
	id, err := in.path()
	if err != nil {
		return WatchNow{}, err
	}

	return watchNow(in.Country, func(country string) (*http.Response, error) {
		return c.API().GetShowsWatchnow(ctx, id, country, &api.GetShowsWatchnowParams{})
	})
}

func fetchEpisodeWatchNow(ctx context.Context, c *trakt.UserClient, in EpisodeWatchNowArgs) (WatchNow, error) {
	id, season, episode, err := in.path()
	if err != nil {
		return WatchNow{}, err
	}

	return watchNow(in.Country, func(country string) (*http.Response, error) {
		return c.API().GetShowsEpisodeWatchnow(ctx, id, season, episode, country, &api.GetShowsEpisodeWatchnowParams{})
	})
}

func fetchWatchNowSources(ctx context.Context, c *trakt.UserClient, _ WatchSourcesArgs) (Listing[WatchSource], error) {
	return fetchParsed(func() (*http.Response, error) {
		return c.API().GetWatchnowSourcesAll(ctx)
	}, parseWatchSources)
}
