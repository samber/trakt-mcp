package tools

import (
	"context"
	"net/http"
	"regexp"
	"slices"
	"strconv"
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

const (
	// maxPriceRunes bounds a price string such as "19.99": Trakt sends it as
	// text, so a hostile value must not flood the model's context.
	maxPriceRunes = 32
	// maxCurrencyRunes bounds a currency code: ISO 4217 codes have 3 letters,
	// the margin tolerates a symbol or a short variant.
	maxCurrencyRunes = 8
	// maxProviderTypeRunes bounds a provider offer type: the longest value
	// the spec lists (tv_everywhere) has 13 characters.
	maxProviderTypeRunes = 32
)

var countryRE = regexp.MustCompile(countryPattern)

// countryArg validates a country code.
func countryArg(s string) (string, error) {
	if !countryRE.MatchString(s) {
		return "", invalidArgument(fieldCountry)
	}

	return s, nil
}

// WatchNowArgs is the country argument of get_watchnow.
type WatchNowArgs struct {
	Country string `json:"country" jsonschema:"two-letter lower-case ISO 3166-1 country code such as fr or us"`
}

// MovieWatchNowArgs are the arguments of the movie handler of get_watchnow.
type MovieWatchNowArgs struct {
	MovieRef
	WatchNowArgs
}

// ShowWatchNowArgs are the arguments of the TV show handler of get_watchnow.
type ShowWatchNowArgs struct {
	ShowRef
	WatchNowArgs
}

// EpisodeWatchNowArgs are the arguments of the episode handler of get_watchnow.
type EpisodeWatchNowArgs struct {
	EpisodeRef
	WatchNowArgs
}

// GetWatchNowArgs are the arguments of get_watchnow.
type GetWatchNowArgs struct {
	ItemRef
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
		Currency:  boundText(deref(l.Currency), maxCurrencyRunes),
		RentPrice: boundText(deref(l.Prices.Rent), maxPriceRunes),
		BuyPrice:  boundText(deref(l.Prices.Purchase), maxPriceRunes),
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

// parseWatchSources returns a decoder that flattens Trakt's list of
// group-keyed maps, groups in alphabetical order so the output is stable. A
// non-empty country keeps only the groups equal to it, ignoring case like
// parseWatchNow; the result is cut at maxReferenceItems.
func parseWatchSources(country string) func([]byte) (Listing[WatchSource], error) {
	return func(body []byte) (Listing[WatchSource], error) {
		return flattenWatchSources(body, country)
	}
}

func flattenWatchSources(body []byte, country string) (Listing[WatchSource], error) {
	raw, err := decodeAs[[]map[string][]traktWatchSource](body)
	if err != nil {
		return Listing[WatchSource]{}, err
	}

	items := []WatchSource{}

	for _, byGroup := range raw {
		groups := make([]string, 0, len(byGroup))

		for group := range byGroup {
			if country == "" || strings.EqualFold(group, country) {
				groups = append(groups, group)
			}
		}

		slices.Sort(groups)

		for _, group := range groups {
			for _, s := range byGroup[group] {
				items = append(items, WatchSource{
					Group: boundText(group, maxTitleRunes), Source: boundText(s.Source, maxTitleRunes), Name: boundText(s.Name, maxTitleRunes),
					Type: boundText(deref(s.Type), maxProviderTypeRunes), Free: s.Free, Cinema: s.Cinema, Amazon: s.Amazon, LinkCount: s.LinkCount,
				})
			}
		}
	}

	return Listing[WatchSource]{Items: capList(items)}, nil
}

// WatchSourcesArgs are the arguments of fetch_watchnow_sources.
type WatchSourcesArgs struct {
	Country string `json:"country,omitempty" jsonschema:"two-letter lower-case ISO 3166-1 country code such as fr or us; keeps only that country's providers"`
}

func registerWatchNowTools(s *Server) {
	Register(s, Spec{
		Name: "get_watchnow", Title: "Get where to watch", Access: Read,
		Description: "List where a movie, TV show or TV show episode can be watched in a country: free, subscription, purchase " +
			"(rent or buy), cable and cinema offers, each with provider and link. country is required. " +
			"type is movie, show (a TV show) or episode (a TV show episode; needs season and episode, season 0 is the specials). " +
			"id is the movie's or the TV show's id.",
	}, getWatchNow)
	Register(s, Spec{
		Name: "fetch_watchnow_sources", Title: "Fetch streaming providers", Access: Read,
		Description: "List the streaming providers Trakt knows, with provider key, name, offer type and the number of titles linked. Use it to read the provider keys of where-to-watch results. Pass country (such as fr) to keep one country's providers: without it the list is very long and is cut at " + strconv.Itoa(maxReferenceItems) + " entries (this server's cap, not Trakt's).",
	}, fetchWatchNowSources)
}

func watchNow(country string, call func(country string) (*http.Response, error)) (WatchNow, error) {
	country, err := countryArg(country)
	if err != nil {
		return WatchNow{}, err
	}

	return fetchParsed(func() (*http.Response, error) { return call(country) }, parseWatchNow(country))
}

func getWatchNow(ctx context.Context, c *trakt.UserClient, in GetWatchNowArgs) (WatchNow, error) {
	return itemFetchers[WatchNow]{
		movie: func(ctx context.Context, c *trakt.UserClient, r MovieRef) (WatchNow, error) {
			return fetchMovieWatchNow(ctx, c, MovieWatchNowArgs{MovieRef: r, WatchNowArgs: in.WatchNowArgs})
		},
		show: func(ctx context.Context, c *trakt.UserClient, r ShowRef) (WatchNow, error) {
			return fetchShowWatchNow(ctx, c, ShowWatchNowArgs{ShowRef: r, WatchNowArgs: in.WatchNowArgs})
		},
		episode: func(ctx context.Context, c *trakt.UserClient, r EpisodeRef) (WatchNow, error) {
			return fetchEpisodeWatchNow(ctx, c, EpisodeWatchNowArgs{EpisodeRef: r, WatchNowArgs: in.WatchNowArgs})
		},
	}.fetch(ctx, c, in.ItemRef)
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

func fetchWatchNowSources(ctx context.Context, c *trakt.UserClient, in WatchSourcesArgs) (Listing[WatchSource], error) {
	var country string

	if in.Country != "" {
		var err error

		if country, err = countryArg(in.Country); err != nil {
			return Listing[WatchSource]{}, err
		}
	}

	return fetchParsed(func() (*http.Response, error) {
		return c.API().GetWatchnowSourcesAll(ctx)
	}, parseWatchSources(country))
}
