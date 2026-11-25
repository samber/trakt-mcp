package tools

import (
	"context"
	"errors"
	"net/http"
	"sort"
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
	// maxFilterEntries caps the keys of a filters object and the elements of
	// a filters array: real filters hold a few dozen keys and short lists, and
	// a cap keeps a hostile definition from flooding the model's context.
	maxFilterEntries = 50
	// maxFilterDepth caps how deep filters nest (the filters object is level
	// 1): real filters are an object of scalars and short arrays.
	maxFilterDepth = 3

	// msgSmartListForbidden answers a Trakt 403 on a smart list read. Trakt
	// sends 403 for a list the connected account may not see, and also when it
	// refuses this server's application key.
	msgSmartListForbidden = "this smart list is private or not shared with the connected Trakt account, or access denied by Trakt"
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

// SmartListsArgs are the arguments of list_smart_lists.
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
	IDs       SmartListIDs `json:"ids"        jsonschema:"pass slug (or the numeric trakt id) as list_id to list_smart_list_items"`
	Privacy   string       `json:"privacy,omitempty"`
	MediaType string       `json:"media_type,omitempty" jsonschema:"movies, shows or both"`
	Source    string       `json:"source,omitempty"`
	CreatedAt string       `json:"created_at,omitempty"`
	UpdatedAt string       `json:"updated_at,omitempty"`
	// Filters are the list's own filters as Trakt names them; always an object.
	Filters map[string]any `json:"filters" jsonschema:"the filters that define the list, as Trakt names them"`
}

// SmartListItemsArgs are the arguments of list_smart_list_items.
type SmartListItemsArgs struct {
	ListID  string `json:"list_id"            jsonschema:"slug or numeric Trakt id of the smart list, from list_smart_lists"`
	Type    string `json:"type,omitempty"     jsonschema:"plural: all, movies or shows (singular movie or show also accepted); narrows a list holding both (default all)"`
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
		Name:  "list_smart_lists",
		Title: "List smart lists",
		Description: "List a user's smart lists (default: the connected user's own): name, ids, privacy, media type, source and the filters that define each. " +
			"Smart lists are dynamic, so this returns definitions, not titles; use list_smart_list_items for those." + untrustedNote,
		Access: Read,
	}, fetchSmartLists)
	Register(s, Spec{
		Name:  "list_smart_list_items",
		Title: "List smart list items",
		Description: "List the movies and TV shows a smart list resolves to right now, optionally of one type, sorted and paginated. " +
			"Take list_id from list_smart_lists. Do not use the normal list tools for smart lists." + untrustedNote,
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

// cleanFilters returns the filters without JSON nulls, with every text value
// bounded, at most maxFilterEntries entries per object or array and at most
// maxFilterDepth levels; a missing object becomes an empty one.
func cleanFilters(in map[string]any) map[string]any { return cleanFilterMap(in, 1) }

func cleanFilterMap(in map[string]any, depth int) map[string]any {
	out := make(map[string]any, len(in))
	if depth > maxFilterDepth {
		return out
	}

	// Sorted keys make the cap deterministic.
	keys := make([]string, 0, len(in))
	for k := range in {
		keys = append(keys, k)
	}

	sort.Strings(keys)

	for _, k := range keys {
		if len(out) >= maxFilterEntries {
			break
		}

		if c := cleanFilterValue(in[k], depth+1); c != nil {
			out[boundText(k, maxFilterTextRunes)] = c
		}
	}

	return out
}

// cleanFilterValue cleans a value found inside a container of level depth-1;
// a container deeper than maxFilterDepth is dropped.
func cleanFilterValue(v any, depth int) any {
	switch x := v.(type) {
	case string:
		return boundText(x, maxFilterTextRunes)
	case []any:
		if depth > maxFilterDepth {
			return nil
		}

		list := make([]any, 0, len(x))

		for _, e := range x {
			if len(list) >= maxFilterEntries {
				break
			}

			if c := cleanFilterValue(e, depth+1); c != nil {
				list = append(list, c)
			}
		}

		return list
	case map[string]any:
		if depth > maxFilterDepth {
			return nil
		}

		return cleanFilterMap(x, depth)
	default:
		return v
	}
}

// smartListError turns a Trakt 403 into the private-list message; every other
// error goes on to the shared mapping.
func smartListError(err error) error {
	if te, ok := errors.AsType[*trakt.Error](err); ok && te.Kind == trakt.KindUnknown && te.Status == http.StatusForbidden {
		// Same log class as other refused calls: a 403 may also be Trakt
		// refusing this server's application key.
		return &toolError{msg: msgSmartListForbidden, class: errorClassTraktRejected}
	}

	return err
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

	out, err := fetchParsed(func() (*http.Response, error) {
		return c.API().GetUsersSmartListsPersonal(ctx, string(id)) //nolint:wrapcheck // *trakt.Error is mapped by Kind
	}, func(body []byte) (Listing[SmartList], error) { return unpaged(body, trimSmartList) })

	return out, smartListError(err)
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

	typ, err := oneOf(fieldType, normalizeMediaFilter(in.Type), mediaAll, smartListTypes)
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

	out, err := paginateDecoded(ctx, in.PageArgs, trimSmartListItem, func(page, limit int) (*http.Response, error) {
		n := strconv.Itoa(limit)

		//nolint:wrapcheck // *trakt.Error is mapped by Kind
		return c.API().GetSmartListsTypedSorted(ctx, string(listID), typ, sortBy, sortHow, &api.GetSmartListsTypedSortedParams{
			Extended: extendedFullPtr(), Page: &page, Limit: &n,
		})
	})

	return out, smartListError(err)
}
