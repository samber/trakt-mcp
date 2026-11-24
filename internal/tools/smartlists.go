package tools

import (
	"context"
	"net/http"
	"strconv"

	"github.com/samber/trakt-mcp/internal/trakt"
	"github.com/samber/trakt-mcp/internal/trakt/api"
)

const (
	fieldUser   = "user"
	fieldListID = "list_id"
	fieldSortBy = "sort_by"

	// userMe is Trakt's name for the authenticated user.
	userMe = "me"
	// smartListSortRank is the order the list's own source gives, the default.
	smartListSortRank = "rank"
	// smartListSortTitle sorts ascending by default, like rank; every other
	// sort is a score or a date, where the best or newest first is the useful
	// default (Trakt's own default for the path).
	smartListSortTitle = "title"
	// maxFilterTextRunes bounds each text value of a smart list's filters:
	// they are short slugs and codes, and a bound keeps hostile text small.
	maxFilterTextRunes = 100
)

var (
	// smartListTypes are the media types Trakt accepts in the items path.
	smartListTypes = []string{mediaAll, mediaMovies, mediaShows}
	// smartListSorts are the sorts Trakt documents for smart list items;
	// "added" applies to watchlist-sourced lists only, and Trakt falls back to
	// rank for a sort it does not know.
	smartListSorts = []string{
		smartListSortRank, "random", smartListSortTitle, "released", "runtime", "percentage", "votes",
		"imdb_rating", "imdb_votes", "tmdb_rating", "tmdb_votes", "rt_tomatometer", "rt_audience", "metascore", "added",
	}
)

// SmartListsArgs are the arguments of fetch_smart_lists.
type SmartListsArgs struct {
	User string `json:"user,omitempty" jsonschema:"Trakt user slug whose smart lists to list (default me, the connected user)"`
}

// SmartListIDs are the identifiers of a smart list.
type SmartListIDs struct {
	Trakt int    `json:"trakt"`
	Slug  string `json:"slug"`
}

// SmartList is the definition of one smart list: what it is, not the items it
// currently resolves to.
type SmartList struct {
	Name      string       `json:"name"`
	IDs       SmartListIDs `json:"ids"        jsonschema:"pass slug (or the numeric trakt id) as list_id to fetch_smart_list_items"`
	Privacy   string       `json:"privacy,omitempty"`
	MediaType string       `json:"media_type,omitempty" jsonschema:"movies, shows or both"`
	Source    string       `json:"source,omitempty"`
	CreatedAt string       `json:"created_at,omitempty"`
	UpdatedAt string       `json:"updated_at,omitempty"`
	// Filters are the list's own filters as Trakt names them; always an object.
	Filters map[string]any `json:"filters" jsonschema:"the filters that define the list, as Trakt names them"`
}

// SmartListItemsArgs are the arguments of fetch_smart_list_items.
type SmartListItemsArgs struct {
	ListID  string `json:"list_id"            jsonschema:"slug or numeric Trakt id of the smart list, from fetch_smart_lists"`
	Type    string `json:"type,omitempty"     jsonschema:"all, movies or shows; narrows a list holding both (default all)"`
	SortBy  string `json:"sort_by,omitempty"  jsonschema:"rank (the list's own order, default), random, title, released, runtime, percentage, votes, imdb_rating, imdb_votes, tmdb_rating, tmdb_votes, rt_tomatometer, rt_audience, metascore or added (watchlist-sourced lists)"`
	SortHow string `json:"sort_how,omitempty" jsonschema:"asc or desc (default asc for rank and title, desc otherwise)"`
	PageArgs
}

// SmartListItem is one title a smart list currently resolves to.
type SmartListItem struct {
	Rank int    `json:"rank"`
	Type string `json:"type" jsonschema:"movie or show"`
	MediaRef
}

func registerSmartListTools(s *Server) {
	Register(s, Spec{
		Name:  "fetch_smart_lists",
		Title: "Fetch smart lists",
		Description: "List a user's smart lists (default: the connected user's own): name, ids, privacy, media type, source and the filters that define each. " +
			"Smart lists are dynamic, so this returns definitions, not titles; use fetch_smart_list_items for those." + untrustedNote,
		Access: Read,
	}, fetchSmartLists)
	Register(s, Spec{
		Name:  "fetch_smart_list_items",
		Title: "Fetch smart list items",
		Description: "List the movies and shows a smart list resolves to right now, optionally of one type, sorted and paginated. " +
			"Take list_id from fetch_smart_lists. Do not use the normal list tools for smart lists." + untrustedNote,
		Access: Read,
	}, fetchSmartListItems)
}

type traktSmartList struct {
	Name      *string        `json:"name"`
	IDs       *traktListIDs  `json:"ids"`
	Privacy   *string        `json:"privacy"`
	MediaType *string        `json:"media_type"`
	Source    *string        `json:"source"`
	CreatedAt *string        `json:"created_at"`
	UpdatedAt *string        `json:"updated_at"`
	Filters   map[string]any `json:"filters"`
}

type traktListIDs struct {
	Trakt *int    `json:"trakt"`
	Slug  *string `json:"slug"`
}

func trimSmartList(l traktSmartList) SmartList {
	out := SmartList{
		Name:      boundText(deref(l.Name), maxTitleRunes),
		Privacy:   boundText(deref(l.Privacy), maxFilterTextRunes),
		MediaType: boundText(deref(l.MediaType), maxFilterTextRunes),
		Source:    boundText(deref(l.Source), maxFilterTextRunes),
		CreatedAt: deref(l.CreatedAt),
		UpdatedAt: deref(l.UpdatedAt),
		Filters:   cleanFilters(l.Filters),
	}

	if l.IDs != nil {
		out.IDs = SmartListIDs{Trakt: deref(l.IDs.Trakt), Slug: boundText(deref(l.IDs.Slug), maxIDLength)}
	}

	return out
}

// cleanFilters returns the filters without JSON nulls and with every text
// value bounded; a missing object becomes an empty one.
func cleanFilters(in map[string]any) map[string]any {
	out := make(map[string]any, len(in))

	for k, v := range in {
		if c := cleanFilterValue(v); c != nil {
			out[boundText(k, maxFilterTextRunes)] = c
		}
	}

	return out
}

func cleanFilterValue(v any) any {
	switch x := v.(type) {
	case string:
		return boundText(x, maxFilterTextRunes)
	case []any:
		list := make([]any, 0, len(x))

		for _, e := range x {
			if c := cleanFilterValue(e); c != nil {
				list = append(list, c)
			}
		}

		return list
	case map[string]any:
		return cleanFilters(x)
	default:
		return v
	}
}

func fetchSmartLists(ctx context.Context, c *trakt.UserClient, in SmartListsArgs) (Listing[SmartList], error) {
	user := in.User
	if user == "" {
		user = userMe
	}

	id, err := idArg(fieldUser, user, Slug)
	if err != nil {
		return Listing[SmartList]{}, err
	}

	return fetchParsed(func() (*http.Response, error) {
		return c.API().GetUsersSmartListsPersonal(ctx, string(id)) //nolint:wrapcheck // *trakt.Error is mapped by Kind
	}, func(body []byte) (Listing[SmartList], error) { return unpaged(body, trimSmartList) })
}

type traktSmartListItem struct {
	Rank int    `json:"rank"`
	Type string `json:"type"`
	traktMedia
}

func trimSmartListItem(i traktSmartListItem) SmartListItem {
	return SmartListItem{Rank: i.Rank, Type: i.Type, MediaRef: i.trim()}
}

func fetchSmartListItems(ctx context.Context, c *trakt.UserClient, in SmartListItemsArgs) (Listing[SmartListItem], error) {
	listID, err := idArg(fieldListID, in.ListID, Slug|Numeric)
	if err != nil {
		return Listing[SmartListItem]{}, err
	}

	typ, err := oneOf(fieldType, in.Type, mediaAll, smartListTypes)
	if err != nil {
		return Listing[SmartListItem]{}, err
	}

	sortBy, err := oneOf(fieldSortBy, in.SortBy, smartListSortRank, smartListSorts)
	if err != nil {
		return Listing[SmartListItem]{}, err
	}

	defaultHow := sortHowDesc
	if sortBy == smartListSortRank || sortBy == smartListSortTitle {
		defaultHow = sortHowAsc
	}

	sortHow, err := oneOf(fieldSortHow, in.SortHow, defaultHow, sortHows)
	if err != nil {
		return Listing[SmartListItem]{}, err
	}

	return paginateDecoded(ctx, in.PageArgs, trimSmartListItem, func(page, limit int) (*http.Response, error) {
		n := strconv.Itoa(limit)

		//nolint:wrapcheck // *trakt.Error is mapped by Kind
		return c.API().GetSmartListsTypedSorted(ctx, string(listID), typ, sortBy, sortHow, &api.GetSmartListsTypedSortedParams{
			Extended: extendedFullPtr(), Page: &page, Limit: &n,
		})
	})
}
