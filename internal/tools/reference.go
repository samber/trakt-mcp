package tools

import (
	"context"
	"net/http"
	"slices"
	"strconv"

	"github.com/samber/trakt-mcp/internal/trakt"
	"github.com/samber/trakt-mcp/internal/trakt/api"
)

const (
	// maxReferenceItems caps each reference list. Trakt does not paginate
	// these lists and states no size limit; the cap is this server's own
	// choice, set above the countries and languages lists, to keep an
	// unexpectedly long list from flooding the model's context.
	maxReferenceItems = 1000
)

// referenceCapNote is appended to every reference tool description so the
// model knows a very long list is cut.
var referenceCapNote = " Not paginated; a list longer than " + strconv.Itoa(maxReferenceItems) + " entries is cut (this server's cap, not Trakt's)."

// referenceTypes lists the media types Trakt accepts for reference data.
var referenceTypes = []string{mediaMovies, mediaShows}

// ReferenceArgs are the arguments of fetch_genres, fetch_countries,
// fetch_languages and fetch_certifications.
type ReferenceArgs struct {
	Type string `json:"type,omitempty" jsonschema:"movies or shows (TV shows); default movies"`
}

// NetworksArgs are the arguments of fetch_networks: none.
type NetworksArgs struct{}

// Genre is one Trakt genre.
type Genre struct {
	Name string `json:"name"`
	Slug string `json:"slug" jsonschema:"genre slug, the value Trakt filters take"`
}

// CodedReference is a country or a language with its lowercase code.
type CodedReference struct {
	Name string `json:"name"`
	Code string `json:"code" jsonschema:"lowercase ISO code, the value Trakt filters take"`
}

// Certification is one content rating of one country.
type Certification struct {
	Country     string `json:"country" jsonschema:"lowercase country code"`
	Name        string `json:"name"`
	Slug        string `json:"slug"`
	Description string `json:"description,omitempty"`
}

// Network is a TV network.
type Network struct {
	Name    string `json:"name"`
	Country string `json:"country,omitempty" jsonschema:"lowercase country code, when Trakt knows it"`
}

type traktGenre struct {
	Name string `json:"name"`
	Slug string `json:"slug"`
}

type traktCoded struct {
	Name string `json:"name"`
	Code string `json:"code"`
}

type traktCertification struct {
	Name        string `json:"name"`
	Slug        string `json:"slug"`
	Description string `json:"description"`
}

type traktNetwork struct {
	Name    string  `json:"name"`
	Country *string `json:"country"`
}

func registerReferenceTools(s *Server) {
	Register(s, Spec{
		Name: "fetch_genres", Title: "Fetch genres", Access: Read,
		Description: "List Trakt's genres (name and slug) for movies or TV shows. Use the slugs as genre filters." + referenceCapNote,
	}, fetchGenres)
	Register(s, Spec{
		Name: "fetch_countries", Title: "Fetch countries", Access: Read,
		Description: "List the countries Trakt knows (name and lowercase code) for movies or TV shows. Use the codes as country arguments." + referenceCapNote,
	}, fetchCountries)
	Register(s, Spec{
		Name: "fetch_languages", Title: "Fetch languages", Access: Read,
		Description: "List the languages Trakt knows (name and lowercase code) for movies or TV shows. Use the codes as language arguments." + referenceCapNote,
	}, fetchLanguages)
	Register(s, Spec{
		Name: "fetch_certifications", Title: "Fetch certifications", Access: Read,
		Description: "List the content ratings (certifications) of every country for movies or TV shows, each with country code, name, slug and description." + referenceCapNote,
	}, fetchCertifications)
	Register(s, Spec{
		Name: "fetch_networks", Title: "Fetch TV networks", Access: Read,
		Description: "List the TV networks Trakt knows, with name and country code. Takes no arguments." + referenceCapNote,
	}, fetchNetworks)
}

// capList cuts a reference list at maxReferenceItems.
func capList[T any](items []T) []T {
	return items[:min(len(items), maxReferenceItems)]
}

// referenceType validates the type argument; movies is the default.
func referenceType(in ReferenceArgs) (string, error) {
	return oneOf(fieldType, in.Type, mediaMovies, referenceTypes)
}

// fetchReference validates the type, then parses the answer of call.
func fetchReference[T any](in ReferenceArgs, call func(typ string) (*http.Response, error), parse func([]byte) (Listing[T], error)) (Listing[T], error) {
	typ, err := referenceType(in)
	if err != nil {
		return Listing[T]{}, err
	}

	return fetchParsed(func() (*http.Response, error) { return call(typ) }, parse)
}

func parseGenres(body []byte) (Listing[Genre], error) {
	l, err := unpaged(body, func(g traktGenre) Genre {
		return Genre{Name: boundText(g.Name, maxTitleRunes), Slug: boundText(g.Slug, maxTitleRunes)}
	})
	l.Items = capList(l.Items)

	return l, err
}

func parseCoded(body []byte) (Listing[CodedReference], error) {
	l, err := unpaged(body, func(c traktCoded) CodedReference {
		return CodedReference{Name: boundText(c.Name, maxTitleRunes), Code: boundText(c.Code, maxTitleRunes)}
	})
	l.Items = capList(l.Items)

	return l, err
}

func parseNetworks(body []byte) (Listing[Network], error) {
	l, err := unpaged(body, func(n traktNetwork) Network {
		return Network{Name: boundText(n.Name, maxTitleRunes), Country: boundText(deref(n.Country), maxTitleRunes)}
	})
	l.Items = capList(l.Items)

	return l, err
}

// parseCertifications flattens Trakt's object keyed by country code into a
// list ordered by country code, then by Trakt's own order.
func parseCertifications(body []byte) (Listing[Certification], error) {
	byCountry, err := decodeAs[map[string][]traktCertification](body)
	if err != nil {
		return Listing[Certification]{}, err
	}

	countries := make([]string, 0, len(byCountry))
	for country := range byCountry {
		countries = append(countries, country)
	}

	slices.Sort(countries)

	items := []Certification{}

	for _, country := range countries {
		for _, c := range byCountry[country] {
			items = append(items, Certification{
				Country:     boundText(country, maxTitleRunes),
				Name:        boundText(c.Name, maxTitleRunes),
				Slug:        boundText(c.Slug, maxTitleRunes),
				Description: boundText(c.Description, maxOverviewRunes),
			})
		}
	}

	return Listing[Certification]{Items: capList(items)}, nil
}

func fetchGenres(ctx context.Context, c *trakt.UserClient, in ReferenceArgs) (Listing[Genre], error) {
	return fetchReference(in, func(typ string) (*http.Response, error) {
		return c.API().GetGenresList(ctx, api.GetGenresListParamsType(typ), &api.GetGenresListParams{})
	}, parseGenres)
}

func fetchCountries(ctx context.Context, c *trakt.UserClient, in ReferenceArgs) (Listing[CodedReference], error) {
	return fetchReference(in, func(typ string) (*http.Response, error) {
		return c.API().GetCountriesList(ctx, api.GetCountriesListParamsType(typ))
	}, parseCoded)
}

func fetchLanguages(ctx context.Context, c *trakt.UserClient, in ReferenceArgs) (Listing[CodedReference], error) {
	return fetchReference(in, func(typ string) (*http.Response, error) {
		return c.API().GetLanguagesList(ctx, api.GetLanguagesListParamsType(typ))
	}, parseCoded)
}

func fetchCertifications(ctx context.Context, c *trakt.UserClient, in ReferenceArgs) (Listing[Certification], error) {
	return fetchReference(in, func(typ string) (*http.Response, error) {
		return c.API().GetCertificationsList(ctx, api.GetCertificationsListParamsType(typ))
	}, parseCertifications)
}

func fetchNetworks(ctx context.Context, c *trakt.UserClient, _ NetworksArgs) (Listing[Network], error) {
	return fetchParsed(func() (*http.Response, error) {
		return c.API().GetNetworksList(ctx)
	}, parseNetworks)
}
