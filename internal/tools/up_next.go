package tools

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"regexp"

	"github.com/samber/trakt-mcp/internal/trakt"
	"github.com/samber/trakt-mcp/internal/trakt/api"
)

const (
	// maxSortByLen bounds sort: Trakt's OpenAPI file documents no values,
	// so the argument is passed through as a short token. UNVERIFIED: not
	// documented by Trakt.
	maxSortByLen = 32
	// hiddenTypeShow is the only type GET /users/hidden/progress_watched
	// accepts (enum show in Trakt's OpenAPI file).
	hiddenTypeShow = "show"
	// progressWatchedSection is the hidden section behind the watched progress
	// views, fixed for the hide tools.
	progressWatchedSection = "progress_watched"
	// progressViewNote is the scope statement of the hide and unhide tools:
	// they never touch what the user watched.
	progressViewNote = " This only changes what the user's TV show progress views display: it never edits watch history."
	// upNextPagingNote tells the caller that Trakt does not count up next pages.
	upNextPagingNote = " Trakt does not report totals for this list: page.page_count and page.item_count are 0, which means unknown, so keep " +
		"requesting the next page until items is empty."
)

// sortByPattern is the shape of the sort value: a lowercase token of letters,
// digits and underscores. It keeps injected query syntax out of the request.
var sortByPattern = regexp.MustCompile(`^[a-z][a-z0-9_]*$`)

// hiddenTypes are the types fetch_hidden_progress accepts.
var hiddenTypes = []string{hiddenTypeShow}

// UpNextArgs are the arguments of fetch_up_next.
type UpNextArgs struct {
	PageArgs

	Sort         string `json:"sort,omitempty"          jsonschema:"a Trakt sort field, as a lowercase token; values are not documented, omit it for Trakt's default"`
	SortHow      string `json:"sort_how,omitempty"      jsonschema:"asc or desc; omit it for Trakt's default"`
	IncludeStats *bool  `json:"include_stats,omitempty" jsonschema:"include minutes watched, minutes left and play count of each show"`
}

// UpNextStats are the watch statistics of an up next entry.
type UpNextStats struct {
	MinutesWatched int `json:"minutes_watched"`
	MinutesLeft    int `json:"minutes_left"`
	PlayCount      int `json:"play_count"`
}

// UpNextEntry is a TV show the user is watching with the episode to watch next.
type UpNextEntry struct {
	Show          Show         `json:"show"`
	Aired         int          `json:"aired"          jsonschema:"episodes aired so far"`
	Completed     int          `json:"completed"      jsonschema:"episodes the user has watched"`
	LastWatchedAt string       `json:"last_watched_at,omitempty"`
	ResetAt       string       `json:"reset_at,omitempty"`
	NextEpisode   *Episode     `json:"next_episode,omitempty"`
	LastEpisode   *Episode     `json:"last_episode,omitempty"`
	Stats         *UpNextStats `json:"stats,omitempty"`
}

type traktUpNext struct {
	Show     traktShow `json:"show"`
	Progress struct {
		Aired         int             `json:"aired"`
		Completed     int             `json:"completed"`
		LastWatchedAt *string         `json:"last_watched_at"`
		ResetAt       *string         `json:"reset_at"`
		NextEpisode   json.RawMessage `json:"next_episode"`
		LastEpisode   json.RawMessage `json:"last_episode"`
		Stats         *struct {
			MinutesWatched int `json:"minutes_watched"`
			MinutesLeft    int `json:"minutes_left"`
			PlayCount      int `json:"play_count"`
		} `json:"stats"`
	} `json:"progress"`
}

func trimUpNext(u traktUpNext) UpNextEntry {
	p := u.Progress
	out := UpNextEntry{
		Show:          trimListShow(u.Show),
		Aired:         p.Aired,
		Completed:     p.Completed,
		LastWatchedAt: deref(p.LastWatchedAt),
		ResetAt:       deref(p.ResetAt),
		NextEpisode:   optionalEpisode(p.NextEpisode),
		LastEpisode:   optionalEpisode(p.LastEpisode),
	}

	if p.Stats != nil {
		out.Stats = &UpNextStats{MinutesWatched: p.Stats.MinutesWatched, MinutesLeft: p.Stats.MinutesLeft, PlayCount: p.Stats.PlayCount}
	}

	return out
}

// HiddenProgressArgs are the arguments of fetch_hidden_progress.
type HiddenProgressArgs struct {
	PageArgs

	Type string `json:"type,omitempty" jsonschema:"show (the only type Trakt lists; default show)"`
}

// HiddenProgressEntry is a TV show or season hidden from watched progress.
type HiddenProgressEntry struct {
	Type     string  `json:"type"               jsonschema:"show or season"`
	HiddenAt string  `json:"hidden_at,omitempty"`
	Show     *Show   `json:"show,omitempty"`
	Season   *Season `json:"season,omitempty"`
}

type traktHiddenProgress struct {
	Type     string       `json:"type"`
	HiddenAt *string      `json:"hidden_at"`
	Show     *traktShow   `json:"show"`
	Season   *traktSeason `json:"season"`
}

func trimHiddenProgress(h traktHiddenProgress) HiddenProgressEntry {
	out := HiddenProgressEntry{Type: boundText(h.Type, maxTitleRunes), HiddenAt: deref(h.HiddenAt)}

	if h.Show != nil {
		s := trimListShow(*h.Show)
		out.Show = &s
	}

	if h.Season != nil {
		s := trimSeason(*h.Season)
		out.Season = &s
	}

	return out
}

// ProgressItemsArgs are the arguments of hide_from_progress and
// unhide_from_progress.
type ProgressItemsArgs struct {
	Items []ItemArg `json:"items" jsonschema:"1 to 50 items, each type show (a TV show) or season; movies and episodes are refused"`
}

// registerUpNextTools registers the up next and hidden progress tools.
func registerUpNextTools(s *Server) {
	Register(s, Spec{
		Name: "fetch_up_next", Title: "Fetch up next", Access: Read,
		Description: "List the TV shows the user is watching, each with the next episode to watch, ordered by sort and sort_how " +
			"(optionally with watch stats)." + upNextPagingNote,
	}, fetchUpNext)
	Register(s, Spec{
		Name: "fetch_hidden_progress", Title: "Fetch hidden progress", Access: Read,
		Description: "List the hidden entries of the user's watched progress: they are hidden TV shows and may include hidden seasons. type is show, the only value Trakt accepts.",
	}, fetchHiddenProgress)
	Register(s, Spec{
		Name: "hide_from_progress", Title: "Hide from progress", Access: Write,
		Description: "Hide TV shows or seasons from the user's watched progress, so they leave the up next and progress lists. " +
			"Items are shows or seasons only." + progressViewNote + " unhide_from_progress reverses it.",
	}, hideFromProgress)
	Register(s, Spec{
		Name: "unhide_from_progress", Title: "Unhide from progress", Access: Write,
		Description: "Show TV shows or seasons again in the user's watched progress after hide_from_progress. " +
			"Items are shows or seasons only." + progressViewNote,
	}, unhideFromProgress)
}

// sortByArg validates sort: empty, or a short lowercase token. Trakt
// documents no values, so no list of them is enforced.
func sortByArg(value string) (*string, error) {
	if value == "" {
		return nil, nil
	}

	if len(value) > maxSortByLen || !sortByPattern.MatchString(value) {
		return nil, invalidArgument(fieldSort)
	}

	return &value, nil
}

func fetchUpNext(ctx context.Context, c *trakt.UserClient, in UpNextArgs) (Listing[UpNextEntry], error) {
	sortBy, err := sortByArg(in.Sort)
	if err != nil {
		return Listing[UpNextEntry]{}, err
	}

	how, err := oneOf(fieldSortHow, in.SortHow, "", sortHows)
	if err != nil {
		return Listing[UpNextEntry]{}, err
	}

	out, err := Paginate(ctx, in.PageArgs, func(page, limit int) ([]UpNextEntry, trakt.Page, error) {
		params := &api.GetSyncProgressUpNextStandardParams{
			Extended: extendedFullPtr(), Page: &page, Limit: &limit, SortBy: sortBy, IncludeStats: in.IncludeStats,
		}

		if how != "" {
			h := api.GetSyncProgressUpNextStandardParamsSortHow(how)
			params.SortHow = &h
		}

		r, err := readReply(func() (*http.Response, error) { return c.API().GetSyncProgressUpNextStandard(ctx, params) })
		if err != nil {
			return nil, trakt.Page{}, err
		}

		items, err := trimAll(r.body, trimUpNext)
		if err != nil {
			return nil, trakt.Page{}, err
		}

		// Trakt's page totals are placeholders: claim one more page so that
		// only an empty page, the limit or maxAutoPages ends the paging.
		return items, trakt.Page{Page: page, Limit: limit, PageCount: page + 1}, nil
	})
	if err != nil {
		return Listing[UpNextEntry]{}, err
	}

	if out.Page != nil {
		// Report the totals as unknown (0), never the placeholders.
		out.Page = &trakt.Page{Page: out.Page.Page, Limit: out.Page.Limit}
	}

	return out, nil
}

func fetchHiddenProgress(ctx context.Context, c *trakt.UserClient, in HiddenProgressArgs) (Listing[HiddenProgressEntry], error) {
	typ, err := oneOf(fieldType, in.Type, hiddenTypeShow, hiddenTypes)
	if err != nil {
		return Listing[HiddenProgressEntry]{}, err
	}

	return paginateDecoded(ctx, in.PageArgs, trimHiddenProgress, func(page, limit int) (*http.Response, error) {
		//nolint:wrapcheck // *trakt.Error is mapped by Kind
		return c.API().GetUsersHiddenGet(ctx, &api.GetUsersHiddenGetParams{
			Extended: extendedFullPtr(), Page: &page, Limit: &limit, Type: api.GetUsersHiddenGetParamsType(typ),
		})
	})
}

// parseProgressItems validates the items of a progress hide or unhide:
// the usual batch rules, then shows and seasons only.
func parseProgressItems(args []ItemArg) ([]Item, error) {
	items, err := parseItems(args)
	if err != nil {
		return nil, err
	}

	for _, it := range items {
		if it.Type != itemShow && it.Type != itemSeason {
			return nil, invalidArgument(fieldItems)
		}
	}

	return items, nil
}

func hideFromProgress(ctx context.Context, c *trakt.UserClient, in ProgressItemsArgs) (WriteResult, error) {
	items, err := parseProgressItems(in.Items)
	if err != nil {
		return WriteResult{}, err
	}

	return writeItems(items, func(body io.Reader) (*http.Response, error) {
		//nolint:wrapcheck // *trakt.Error is mapped by Kind
		return c.API().PostUsersHiddenAddWithBody(ctx, api.PostUsersHiddenAddParamsSection(progressWatchedSection), jsonContentType, body)
	})
}

func unhideFromProgress(ctx context.Context, c *trakt.UserClient, in ProgressItemsArgs) (WriteResult, error) {
	items, err := parseProgressItems(in.Items)
	if err != nil {
		return WriteResult{}, err
	}

	return writeItems(items, func(body io.Reader) (*http.Response, error) {
		//nolint:wrapcheck // *trakt.Error is mapped by Kind
		return c.API().PostUsersHiddenRemoveSectionWithBody(ctx, api.PostUsersHiddenRemoveSectionParamsSection(progressWatchedSection), jsonContentType, body)
	})
}
