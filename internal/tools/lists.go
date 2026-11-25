package tools

import (
	"context"
	"net/http"
	"strconv"

	"github.com/samber/trakt-mcp/internal/trakt"
	"github.com/samber/trakt-mcp/internal/trakt/api"
)

const (
	// mediaPeople selects the people of a custom list in the items path.
	mediaPeople = "people"
	// itemPerson is the type of a list item that is a person.
	itemPerson = "person"
	// maxListFieldRunes bounds the short enum-like text of a list (privacy,
	// type, sort): Trakt's longest values have under 20 characters.
	maxListFieldRunes = 32
)

var (
	// listItemTypes are the item type filters of the custom list items path.
	listItemTypes = []string{mediaAll, mediaMovies, mediaShows, mediaSeasons, mediaEpisodes, mediaPeople}
	// listItemSorts are the sorts of the list items path. Trakt's documentation
	// of the list sorts is not in the filtered spec; this is the set the
	// watchlist path takes, unverified for custom lists (see task 5 report).
	listItemSorts = watchlistSorts
	// popularListTypes are the type filters of the trending and popular list
	// paths; omitting the type returns both kinds.
	popularListTypes = []string{listTypePersonal, listTypeOfficial}
)

// UserListsArgs are the arguments of fetch_user_lists.
type UserListsArgs struct {
	User string `json:"user,omitempty" jsonschema:"Trakt user slug or numeric id whose lists to list (default me, the connected user)"`
}

// ListArgs are the arguments of fetch_list.
type ListArgs struct {
	ListID string `json:"list_id"        jsonschema:"slug or numeric Trakt id of the list"`
	User   string `json:"user,omitempty" jsonschema:"Trakt user slug or numeric id owning the list; omit it for an official or public list looked up by id alone"`
}

// ListItemsArgs are the arguments of fetch_list_items.
type ListItemsArgs struct {
	ListArgs
	Type    string `json:"type,omitempty"     jsonschema:"all, movies, shows, seasons, episodes or people (default all)"`
	Sort    string `json:"sort,omitempty"     jsonschema:"rank, added, title, released, runtime, popularity, percentage or votes (default rank)"`
	SortHow string `json:"sort_how,omitempty" jsonschema:"asc or desc (default asc)"`
	PageArgs
}

// PopularListsArgs are the arguments of fetch_trending_lists and
// fetch_popular_lists.
type PopularListsArgs struct {
	Type string `json:"type,omitempty" jsonschema:"personal or official; omit it for both"`
	PageArgs
}

// ListDetail is a custom list as Trakt describes it. Name, description and
// owner are third-party text.
type ListDetail struct {
	TraktList
	Privacy        string `json:"privacy,omitempty"         jsonschema:"private, link, friends or public"`
	Type           string `json:"type,omitempty"            jsonschema:"personal or official"`
	SortBy         string `json:"sort_by,omitempty"         jsonschema:"how the list is sorted by default"`
	SortHow        string `json:"sort_how,omitempty"        jsonschema:"asc or desc"`
	CreatedAt      string `json:"created_at,omitempty"`
	AllowComments  bool   `json:"allow_comments,omitempty"`
	DisplayNumbers bool   `json:"display_numbers,omitempty" jsonschema:"whether the list shows item ranks"`
}

func (ListDetail) resultCount() int { return 1 }

// TrendingList is an entry of the trending and popular lists, with the
// counters Trakt ranks it by.
type TrendingList struct {
	LikeCount    int        `json:"like_count"`
	CommentCount int        `json:"comment_count"`
	List         ListDetail `json:"list"`
}

// ListItem is one entry of a custom list: a movie, show, season or episode
// (with its show) in the matching field, or a person. Notes are the list
// owner's text.
type ListItem struct {
	Rank     int     `json:"rank"`
	ID       int     `json:"list_item_id"       jsonschema:"Trakt's id of this list entry, not of the title"`
	ListedAt string  `json:"listed_at,omitempty"`
	Type     string  `json:"type"                jsonschema:"movie, show, season, episode or person"`
	Notes    string  `json:"notes,omitempty"     jsonschema:"the list owner's note on this entry"`
	Person   *Person `json:"person,omitempty"`
	MediaRef
}

func registerListTools(s *Server) {
	Register(s, Spec{
		Name: "fetch_user_lists", Title: "Fetch user lists", Access: Read,
		Description: "List a user's custom lists (default: the connected user's own) with name, description, privacy, item count and likes. " +
			"Take list ids from the result for fetch_list and fetch_list_items." + untrustedNote,
	}, fetchUserLists)
	Register(s, Spec{
		Name: "fetch_list", Title: "Fetch list", Access: Read,
		Description: "Get one custom list: name, description, privacy, sort, item count and likes. " +
			"Give user for a user's list; omit it to look a list up by its id alone." + untrustedNote,
	}, fetchList)
	Register(s, Spec{
		Name: "fetch_list_items", Title: "Fetch list items", Access: Read,
		Description: "List the entries of a custom list: movies, TV shows, seasons, episodes and people, optionally of one type, sorted and paginated, with the owner's notes. " +
			"Give user for a user's list; omit it to look a list up by its id alone." + untrustedNote,
	}, fetchListItems)
	Register(s, Spec{
		Name: "fetch_trending_lists", Title: "Fetch trending lists", Access: Read,
		Description: "List the custom lists Trakt users are liking and commenting on right now, optionally only personal or official ones. Takes limit and page." + untrustedNote,
	}, fetchTrendingLists)
	Register(s, Spec{
		Name: "fetch_popular_lists", Title: "Fetch popular lists", Access: Read,
		Description: "List the most popular custom lists on Trakt, optionally only personal or official ones. Takes limit and page." + untrustedNote,
	}, fetchPopularLists)
}

type traktListDetail struct {
	traktList
	Privacy        *string `json:"privacy"`
	Type           *string `json:"type"`
	SortBy         *string `json:"sort_by"`
	SortHow        *string `json:"sort_how"`
	CreatedAt      *string `json:"created_at"`
	AllowComments  bool    `json:"allow_comments"`
	DisplayNumbers bool    `json:"display_numbers"`
}

func trimListDetail(l traktListDetail) ListDetail {
	out := trimList(l.traktList)
	out.Owner = boundText(out.Owner, maxTitleRunes)

	return ListDetail{
		TraktList:      out,
		Privacy:        boundText(deref(l.Privacy), maxListFieldRunes),
		Type:           boundText(deref(l.Type), maxListFieldRunes),
		SortBy:         boundText(deref(l.SortBy), maxListFieldRunes),
		SortHow:        boundText(deref(l.SortHow), maxListFieldRunes),
		CreatedAt:      deref(l.CreatedAt),
		AllowComments:  l.AllowComments,
		DisplayNumbers: l.DisplayNumbers,
	}
}

func parseListDetail(body []byte) (ListDetail, error) {
	raw, err := decodeAs[traktListDetail](body)
	if err != nil {
		return ListDetail{}, err
	}

	return trimListDetail(raw), nil
}

type traktTrendingList struct {
	LikeCount    int             `json:"like_count"`
	CommentCount int             `json:"comment_count"`
	List         traktListDetail `json:"list"`
}

func trimTrendingList(t traktTrendingList) TrendingList {
	return TrendingList{LikeCount: t.LikeCount, CommentCount: t.CommentCount, List: trimListDetail(t.List)}
}

type traktListItem struct {
	Rank     int          `json:"rank"`
	ID       int          `json:"id"`
	ListedAt *string      `json:"listed_at"`
	Type     string       `json:"type"`
	Notes    *string      `json:"notes"`
	Person   *traktPerson `json:"person"`
	traktMedia
}

func trimListItem(i traktListItem) ListItem {
	out := ListItem{
		Rank:     i.Rank,
		ID:       i.ID,
		ListedAt: deref(i.ListedAt),
		Type:     boundText(i.Type, maxListFieldRunes),
		Notes:    boundText(deref(i.Notes), maxNotesRunes),
		MediaRef: i.trim(),
	}

	if i.Person != nil {
		p := trimEmbeddedPerson(*i.Person)
		out.Person = &p
	}

	return out
}

// listUser validates the optional user of a list tool: "" means the list is
// looked up without an owner.
func listUser(raw string) (string, error) {
	if raw == "" {
		return "", nil
	}

	id, err := idArg(fieldUser, raw, Slug|Numeric)

	return string(id), err
}

func fetchUserLists(ctx context.Context, c *trakt.UserClient, in UserListsArgs) (Listing[ListDetail], error) {
	user := in.User
	if user == "" {
		user = userMe
	}

	id, err := idArg(fieldUser, user, Slug|Numeric)
	if err != nil {
		return Listing[ListDetail]{}, err
	}

	return fetchParsed(func() (*http.Response, error) {
		return c.API().GetUsersListsPersonal(ctx, string(id), &api.GetUsersListsPersonalParams{})
	}, func(body []byte) (Listing[ListDetail], error) { return unpaged(body, trimListDetail) })
}

// path validates the list id and the optional user of a list tool.
func (a ListArgs) path() (user, listID string, err error) {
	user, err = listUser(a.User)
	if err != nil {
		return "", "", err
	}

	id, err := idArg(fieldListID, a.ListID, Slug|Numeric)

	return user, string(id), err
}

func fetchList(ctx context.Context, c *trakt.UserClient, in ListArgs) (ListDetail, error) {
	user, listID, err := in.path()
	if err != nil {
		return ListDetail{}, err
	}

	return fetchParsed(func() (*http.Response, error) {
		if user == "" {
			return c.API().GetListsSummary(ctx, listID, &api.GetListsSummaryParams{})
		}

		return c.API().GetUsersListsListSummary(ctx, user, listID, &api.GetUsersListsListSummaryParams{})
	}, parseListDetail)
}

func fetchListItems(ctx context.Context, c *trakt.UserClient, in ListItemsArgs) (Listing[ListItem], error) {
	user, listID, err := in.path()
	if err != nil {
		return Listing[ListItem]{}, err
	}

	typ, err := oneOf(fieldType, in.Type, mediaAll, listItemTypes)
	if err != nil {
		return Listing[ListItem]{}, err
	}

	sortBy, err := oneOf(fieldSort, in.Sort, watchlistSortRank, listItemSorts)
	if err != nil {
		return Listing[ListItem]{}, err
	}

	sortHow, err := oneOf(fieldSortHow, in.SortHow, sortHowAsc, sortHows)
	if err != nil {
		return Listing[ListItem]{}, err
	}

	return paginateDecoded(ctx, in.PageArgs, trimListItem, func(page, limit int) (*http.Response, error) {
		n := strconv.Itoa(limit)

		if user == "" {
			return c.API().GetListsItemsTypedSorted(ctx, listID, typ, sortBy, sortHow,
				&api.GetListsItemsTypedSortedParams{Extended: extendedFullPtr(), Page: &page, Limit: &n})
		}

		return c.API().GetUsersListsListItemsTypedSorted(ctx, user, listID, typ, sortBy, sortHow,
			&api.GetUsersListsListItemsTypedSortedParams{Extended: extendedFullPtr(), Page: &page, Limit: &n})
	})
}

func fetchTrendingLists(ctx context.Context, c *trakt.UserClient, in PopularListsArgs) (Listing[TrendingList], error) {
	return rankedLists(ctx, c, in, true)
}

func fetchPopularLists(ctx context.Context, c *trakt.UserClient, in PopularListsArgs) (Listing[TrendingList], error) {
	return rankedLists(ctx, c, in, false)
}

// rankedLists validates the type of a trending or popular lists call and
// pages through the path without a type, or the one with it.
func rankedLists(ctx context.Context, c *trakt.UserClient, in PopularListsArgs, trending bool) (Listing[TrendingList], error) {
	typ, err := oneOf(fieldType, in.Type, "", popularListTypes)
	if err != nil {
		return Listing[TrendingList]{}, err
	}

	return paginateDecoded(ctx, in.PageArgs, trimTrendingList, func(page, limit int) (*http.Response, error) {
		switch {
		case trending && typ == "":
			return c.API().GetListsTrending(ctx, &api.GetListsTrendingParams{Page: &page, Limit: &limit})
		case trending:
			return c.API().GetListsTrendingByType(ctx, typ, &api.GetListsTrendingByTypeParams{Page: &page, Limit: &limit})
		case typ == "":
			return c.API().GetListsPopular(ctx, &api.GetListsPopularParams{Page: &page, Limit: &limit})
		default:
			return c.API().GetListsPopularByType(ctx, typ, &api.GetListsPopularByTypeParams{Page: &page, Limit: &limit})
		}
	})
}
