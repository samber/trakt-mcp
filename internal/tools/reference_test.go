package tools

import (
	"fmt"
	"net/http"
	"strings"
	"testing"
)

const (
	toolFetchGenres         = "fetch_genres"
	toolFetchCountries      = "fetch_countries"
	toolFetchLanguages      = "fetch_languages"
	toolFetchCertifications = "fetch_certifications"
	toolFetchNetworks       = "fetch_networks"

	routeGenres         = "GET /genres/movies"
	routeCountries      = "GET /countries/movies"
	routeLanguages      = "GET /languages/movies"
	routeCertifications = "GET /certifications/movies"
	routeNetworks       = "GET /networks"

	pathFirstTitleName = "items.0.name"
	pathFirstSlug      = "items.0.slug"
	pathFirstCode      = "items.0.code"
)

func init() { catalogCaseSets = append(catalogCaseSets, referenceCases) }

func typeArg(v string) map[string]any { return map[string]any{fieldType: v} }

var referenceCases = []catalogCase{
	{
		tool: toolFetchGenres, args: map[string]any{}, route: routeGenres, fixture: "reference_genres",
		want:   map[string]any{pathFirstTitleName: "Horror", pathFirstSlug: "horror", "items.1.slug": "western"},
		absent: []string{"items.1.subgenres", fieldPage},
	},
	{
		tool: toolFetchCountries, args: map[string]any{}, route: routeCountries, fixture: "reference_countries",
		want: map[string]any{pathFirstTitleName: "France", pathFirstCode: "fr", "items.1.code": "us"},
	},
	{
		tool: toolFetchLanguages, args: map[string]any{}, route: routeLanguages, fixture: "reference_languages",
		want: map[string]any{pathFirstTitleName: "French", pathFirstCode: "fr", pathSecondName: "English"},
	},
	{
		tool: toolFetchCertifications, args: map[string]any{}, route: routeCertifications, fixture: "reference_certifications", object: true,
		want: map[string]any{
			pathFirstCountry: "fr", pathFirstTitleName: "U", pathFirstSlug: "u", "items.0.description": "All audiences",
			pathSecondCountry: "us", pathSecondName: "PG-13", "items.2.country": "us", "items.2.slug": "r",
		},
	},
	{
		tool: toolFetchNetworks, args: map[string]any{}, route: routeNetworks, fixture: "reference_networks",
		want: map[string]any{pathFirstTitleName: "Showtime", pathFirstCountry: "us", pathSecondName: "Unknown Net"},
		// A network without a country omits the field, never null.
		absent: []string{pathSecondCountry},
	},
}

func TestReferenceToolArguments(t *testing.T) {
	t.Parallel()

	cases := []argCase{
		{name: "genres shows", tool: toolFetchGenres, args: typeArg(mediaShows), route: "GET /genres/shows", body: emptyList, wantPath: "/genres/shows"},
		{name: "genres default movies", tool: toolFetchGenres, args: typeArg(""), route: routeGenres, body: emptyList, wantPath: "/genres/movies"},
		{name: "countries shows", tool: toolFetchCountries, args: typeArg(mediaShows), route: "GET /countries/shows", body: emptyList, wantPath: "/countries/shows"},
		{name: "languages shows", tool: toolFetchLanguages, args: typeArg(mediaShows), route: "GET /languages/shows", body: emptyList, wantPath: "/languages/shows"},
		{name: "certifications shows", tool: toolFetchCertifications, args: typeArg(mediaShows), route: "GET /certifications/shows", body: emptyObject, wantPath: "/certifications/shows"},
		{name: "type unknown", tool: toolFetchGenres, args: typeArg("x"), wantErr: invalid(fieldType)},
		{name: "type episodes", tool: toolFetchCountries, args: typeArg(mediaEpisodes), wantErr: invalid(fieldType)},
		{name: "type wrong case", tool: toolFetchLanguages, args: typeArg("Movies"), wantErr: invalid(fieldType)},
		{name: "type traversal", tool: toolFetchCertifications, args: typeArg(traversalID), wantErr: invalid(fieldType)},
	}

	runArgCases(t, cases)
}

// TestReferenceListsAreCapped requires a Trakt list longer than the cap to be
// cut at maxReferenceItems.
func TestReferenceListsAreCapped(t *testing.T) {
	t.Parallel()

	var b strings.Builder

	b.WriteString("[")

	for i := range maxReferenceItems + 1 {
		if i > 0 {
			b.WriteString(",")
		}

		fmt.Fprintf(&b, `{"name":"Net %d","country":"us"}`, i)
	}

	b.WriteString("]")

	h := newHarness(t)
	h.trakt.respond(routeNetworks, http.StatusOK, b.String(), nil)

	var out struct {
		Items []map[string]any `json:"items"`
	}

	h.ok(toolFetchNetworks, map[string]any{}, &out)

	if len(out.Items) != maxReferenceItems {
		t.Errorf("items = %d, want the cap %d", len(out.Items), maxReferenceItems)
	}
}

// TestReferenceTextIsBounded requires long Trakt text to be cut.
func TestReferenceTextIsBounded(t *testing.T) {
	t.Parallel()

	long := strings.Repeat("x", maxTitleRunes*3)

	h := newHarness(t)
	h.trakt.respond(routeNetworks, http.StatusOK, `[{"name":"`+long+`","country":"us"}]`, nil)

	var out struct {
		Items []struct {
			Name string `json:"name"`
		} `json:"items"`
	}

	h.ok(toolFetchNetworks, map[string]any{}, &out)

	if len(out.Items) != 1 || len([]rune(out.Items[0].Name)) > maxTitleRunes {
		t.Errorf("name length = %d, want at most %d", len([]rune(out.Items[0].Name)), maxTitleRunes)
	}
}

func TestReferenceTraktFailures(t *testing.T) {
	t.Parallel()

	runTraktFailureMatrix(t, []failureTool{
		{toolFetchGenres, map[string]any{}, routeGenres, true},
		{toolFetchCountries, map[string]any{}, routeCountries, true},
		{toolFetchLanguages, map[string]any{}, routeLanguages, true},
		{toolFetchCertifications, map[string]any{}, routeCertifications, true},
		{toolFetchNetworks, map[string]any{}, routeNetworks, true},
	})
}
