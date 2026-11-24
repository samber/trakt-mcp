package tools

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/samber/trakt-mcp/internal/trakt"
)

// Item types the batch write tools accept (spec §8.4).
const (
	itemMovie   = "movie"
	itemShow    = "show"
	itemSeason  = "season"
	itemEpisode = "episode"
)

// Plural media types, as Trakt names them in sync paths and request bodies.
const (
	mediaMovies   = "movies"
	mediaShows    = "shows"
	mediaSeasons  = "seasons"
	mediaEpisodes = "episodes"
	// mediaAll selects every type in the watchlist and ratings paths.
	mediaAll = "all"
)

const (
	// maxBatchItems caps the items of one write call (spec §8.4): the call
	// is one Trakt request, and a bound keeps a mistaken bulk write small.
	maxBatchItems = 50
	// maxFutureSkew is how far ahead a watched_at or collected_at may be:
	// enough for clock drift and time zones, while a later date is a mistake.
	maxFutureSkew = 24 * time.Hour
	// watchedUnknown is Trakt's watched_at for a viewing of unknown date.
	watchedUnknown = "unknown"
	// watchedReleased is Trakt's watched_at for "when it aired"; Trakt takes
	// it for episodes only (spec §8.4).
	watchedReleased = "released"
	// traktTimeLayout is the UTC timestamp form Trakt sends and accepts.
	traktTimeLayout = "2006-01-02T15:04:05.000Z"
	// jsonContentType is the body type of every Trakt write.
	jsonContentType = "application/json"
)

// Argument names, as they appear in tool errors.
const (
	fieldItems       = "items"
	fieldWatchedAt   = "watched_at"
	fieldCollectedAt = "collected_at"
	fieldType        = "type"
)

// ItemArg is one item of a batch write tool, as the caller sends it.
type ItemArg struct {
	Type string `json:"type" jsonschema:"movie, show, season or episode"`
	ID   string `json:"id"   jsonschema:"numeric Trakt id or IMDb id (tt0903747); a Trakt slug (breaking-bad) also works for movie and show"`
}

// ItemsArgs are the arguments of a batch write tool that takes items only.
type ItemsArgs struct {
	Items []ItemArg `json:"items" jsonschema:"1 to 50 items; a show item covers the whole show, a season or episode item only that one"`
}

// Item is a validated item of a batch write. Only Type and ID are returned
// to the caller (in not_found); the other fields are request options.
type Item struct {
	Type        string `json:"type"`
	ID          ID     `json:"id"`
	WatchedAt   string `json:"-"`
	CollectedAt string `json:"-"`
	Rating      *int   `json:"-"`
	Notes       string `json:"-"`
}

// SyncItems is the body of Trakt's batch sync writes: items grouped by type.
type SyncItems struct {
	Movies   []SyncEntry `json:"movies,omitempty"`
	Shows    []SyncEntry `json:"shows,omitempty"`
	Seasons  []SyncEntry `json:"seasons,omitempty"`
	Episodes []SyncEntry `json:"episodes,omitempty"`
}

// SyncEntry is one item of a sync body with its options.
type SyncEntry struct {
	IDs         SyncIDs `json:"ids"`
	WatchedAt   string  `json:"watched_at,omitempty"`
	CollectedAt string  `json:"collected_at,omitempty"`
	Rating      *int    `json:"rating,omitempty"`
	Notes       string  `json:"notes,omitempty"`
}

// SyncIDs identifies an item to Trakt by exactly one id.
type SyncIDs struct {
	Trakt int64  `json:"trakt,omitempty"`
	Slug  string `json:"slug,omitempty"`
	IMDb  string `json:"imdb,omitempty"`
}

// syncIDsOf turns a validated ID into the single id Trakt matches it by.
func syncIDsOf(id ID) (SyncIDs, error) {
	s := string(id)

	switch {
	case imdbPattern.MatchString(s):
		return SyncIDs{IMDb: s}, nil
	case numericPattern.MatchString(s):
		n, err := strconv.ParseInt(s, 10, 64)
		if err != nil || n <= 0 {
			return SyncIDs{}, errInvalidID
		}

		return SyncIDs{Trakt: n}, nil
	case s != "":
		return SyncIDs{Slug: s}, nil
	}

	return SyncIDs{}, errInvalidID
}

// itemKinds is the id forms each item type accepts: Trakt has no season or
// episode slugs.
var itemKinds = map[string]IDKinds{
	itemMovie:   Slug | Numeric | IMDbTitle,
	itemShow:    Slug | Numeric | IMDbTitle,
	itemSeason:  Numeric | IMDbTitle,
	itemEpisode: Numeric | IMDbTitle,
}

// parseItems validates the items of a batch write: 1 to maxBatchItems items
// of a known type with an id of a form that type accepts.
func parseItems(args []ItemArg) ([]Item, error) {
	if len(args) == 0 || len(args) > maxBatchItems {
		return nil, invalidArgument(fieldItems)
	}

	items := make([]Item, 0, len(args))

	for _, a := range args {
		kinds, ok := itemKinds[a.Type]
		if !ok {
			return nil, invalidArgument(fieldItems)
		}

		id, err := ParseID(a.ID, kinds)
		if err != nil {
			return nil, invalidArgument(fieldItems)
		}

		if _, err := syncIDsOf(id); err != nil {
			return nil, invalidArgument(fieldItems)
		}

		items = append(items, Item{Type: a.Type, ID: id})
	}

	return items, nil
}

// GroupItems builds the sync body of items: each goes to the array of its
// type, in the caller's order.
func GroupItems(items []Item) (SyncItems, error) {
	if len(items) == 0 || len(items) > maxBatchItems {
		return SyncItems{}, invalidArgument(fieldItems)
	}

	var out SyncItems

	for _, it := range items {
		ids, err := syncIDsOf(it.ID)
		if err != nil {
			return SyncItems{}, invalidArgument(fieldItems)
		}

		e := SyncEntry{IDs: ids, WatchedAt: it.WatchedAt, CollectedAt: it.CollectedAt, Rating: it.Rating, Notes: it.Notes}

		switch it.Type {
		case itemMovie:
			out.Movies = append(out.Movies, e)
		case itemShow:
			out.Shows = append(out.Shows, e)
		case itemSeason:
			out.Seasons = append(out.Seasons, e)
		case itemEpisode:
			out.Episodes = append(out.Episodes, e)
		default:
			return SyncItems{}, invalidArgument(fieldItems)
		}
	}

	return out, nil
}

// parseTimestamp validates an ISO 8601 timestamp (RFC 3339, any offset) not
// more than maxFutureSkew ahead, and returns it in traktTimeLayout.
func parseTimestamp(field, value string) (string, error) {
	return parseTimestampWithin(field, value, maxFutureSkew)
}

// parseTimestampWithin is parseTimestamp with an explicit clock tolerance.
func parseTimestampWithin(field, value string, skew time.Duration) (string, error) {
	t, err := time.Parse(time.RFC3339Nano, value)
	if err != nil || t.After(time.Now().Add(skew)) {
		return "", invalidArgument(field)
	}

	return t.UTC().Format(traktTimeLayout), nil
}

// watchedAtArg validates watched_at for items: empty (now), a timestamp,
// unknown, or released when every item is an episode.
func watchedAtArg(value string, items []Item) (string, error) {
	switch value {
	case "", watchedUnknown:
		return value, nil
	case watchedReleased:
		for _, it := range items {
			if it.Type != itemEpisode {
				return "", invalidArgument(fieldWatchedAt)
			}
		}

		return value, nil
	}

	return parseTimestamp(fieldWatchedAt, value)
}

// WriteResult is the outcome of a batch write: how many items Trakt added,
// deleted or already had, and the caller's items it did not find.
type WriteResult struct {
	Added    int    `json:"added"`
	Deleted  int    `json:"deleted"`
	Existing int    `json:"existing"`
	NotFound []Item `json:"not_found" jsonschema:"items Trakt did not find, as the caller sent them"`
}

func (r WriteResult) resultCount() int { return r.Added + r.Deleted + r.Existing }

// traktCounts are Trakt's per-type counters of a batch write.
type traktCounts struct {
	Movies   int `json:"movies"`
	Shows    int `json:"shows"`
	Seasons  int `json:"seasons"`
	Episodes int `json:"episodes"`
}

func (c traktCounts) total() int { return c.Movies + c.Shows + c.Seasons + c.Episodes }

// traktRef is an item Trakt echoes back in not_found.
type traktRef struct {
	IDs struct {
		Trakt *int64  `json:"trakt"`
		Slug  *string `json:"slug"`
		IMDb  *string `json:"imdb"`
	} `json:"ids"`
}

type traktWriteReply struct {
	Added    traktCounts `json:"added"`
	Deleted  traktCounts `json:"deleted"`
	Existing traktCounts `json:"existing"`
	NotFound struct {
		Movies   []traktRef `json:"movies"`
		Shows    []traktRef `json:"shows"`
		Seasons  []traktRef `json:"seasons"`
		Episodes []traktRef `json:"episodes"`
	} `json:"not_found"`
}

// writeItems sends items as one Trakt batch write and reads the outcome.
// It never splits the batch: parseItems already bounds it.
func writeItems(items []Item, send func(body io.Reader) (*http.Response, error)) (WriteResult, error) {
	body, err := GroupItems(items)
	if err != nil {
		return WriteResult{}, err
	}

	data, err := json.Marshal(body)
	if err != nil {
		return WriteResult{}, fmt.Errorf("encode sync body: %w", err)
	}

	r, err := readReply(func() (*http.Response, error) { return send(bytes.NewReader(data)) })
	if err != nil {
		return WriteResult{}, err
	}

	raw, err := decodeAs[traktWriteReply](r.body)
	if err != nil {
		return WriteResult{}, err
	}

	notFound := matchNotFound(items, itemMovie, raw.NotFound.Movies, nil)
	notFound = matchNotFound(items, itemShow, raw.NotFound.Shows, notFound)
	notFound = matchNotFound(items, itemSeason, raw.NotFound.Seasons, notFound)
	notFound = matchNotFound(items, itemEpisode, raw.NotFound.Episodes, notFound)

	return WriteResult{
		Added:    raw.Added.total(),
		Deleted:  raw.Deleted.total(),
		Existing: raw.Existing.total(),
		NotFound: NonNil(notFound),
	}, nil
}

// matchNotFound appends to out the caller's items of type typ that Trakt
// reported in refs. A ref matching no caller item is kept only when its id
// is a valid identifier: Trakt's echo is untrusted text.
func matchNotFound(items []Item, typ string, refs []traktRef, out []Item) []Item {
	for _, ref := range refs {
		if it, ok := findItem(items, typ, ref); ok {
			if !containsItem(out, it) {
				out = append(out, it)
			}

			continue
		}

		if id, ok := refID(ref); ok && !containsItem(out, Item{Type: typ, ID: id}) {
			out = append(out, Item{Type: typ, ID: id})
		}
	}

	return out
}

func findItem(items []Item, typ string, ref traktRef) (Item, bool) {
	for _, it := range items {
		if it.Type != typ {
			continue
		}

		s := string(it.ID)
		if (ref.IDs.Trakt != nil && strconv.FormatInt(*ref.IDs.Trakt, 10) == s) ||
			(ref.IDs.Slug != nil && *ref.IDs.Slug == s) ||
			(ref.IDs.IMDb != nil && *ref.IDs.IMDb == s) {
			return Item{Type: it.Type, ID: it.ID}, true
		}
	}

	return Item{}, false
}

func containsItem(items []Item, it Item) bool {
	for _, x := range items {
		if x.Type == it.Type && x.ID == it.ID {
			return true
		}
	}

	return false
}

// refID returns the first valid id of a ref Trakt echoed back.
func refID(ref traktRef) (ID, bool) {
	if ref.IDs.Trakt != nil && *ref.IDs.Trakt > 0 {
		return ID(strconv.FormatInt(*ref.IDs.Trakt, 10)), true
	}

	for _, s := range []*string{ref.IDs.IMDb, ref.IDs.Slug} {
		if s == nil {
			continue
		}

		if id, err := ParseID(*s, Slug|IMDbTitle); err == nil {
			return id, true
		}
	}

	return "", false
}

// MediaRef is the movie, show, season or episode an account entry is
// about. Only the fields Trakt sent are present; a season or an episode
// comes with its show.
type MediaRef struct {
	Movie   *Movie   `json:"movie,omitempty"`
	Show    *Show    `json:"show,omitempty"`
	Season  *Season  `json:"season,omitempty"`
	Episode *Episode `json:"episode,omitempty"`
}

// traktMedia is the media part of a Trakt account entry.
type traktMedia struct {
	Movie   *traktMovie   `json:"movie"`
	Show    *traktShow    `json:"show"`
	Season  *traktSeason  `json:"season"`
	Episode *traktEpisode `json:"episode"`
}

func (m traktMedia) trim() MediaRef {
	var out MediaRef

	if m.Movie != nil {
		v := trimListMovie(*m.Movie)
		out.Movie = &v
	}

	if m.Show != nil {
		v := trimListShow(*m.Show)
		out.Show = &v
	}

	if m.Season != nil {
		v := trimSeason(*m.Season)
		out.Season = &v
	}

	if m.Episode != nil {
		v := trimEpisode(*m.Episode)
		out.Episode = &v
	}

	return out
}

// localPage pages items the tool holds in full (Trakt endpoints that are
// not paginated) the way Paginate pages Trakt's lists.
func localPage[T any](ctx context.Context, args PageArgs, items []T) (Listing[T], error) {
	return Paginate(ctx, args, func(page, limit int) ([]T, trakt.Page, error) {
		meta := trakt.Page{Page: page, Limit: limit, ItemCount: len(items), PageCount: int(math.Ceil(float64(len(items)) / float64(limit)))}

		// Compared by division first: (page-1)*limit overflows for a huge page.
		if page-1 > len(items)/limit {
			return []T{}, meta, nil
		}

		start := (page - 1) * limit
		if start >= len(items) {
			return []T{}, meta, nil
		}

		return items[start:min(start+limit, len(items))], meta, nil
	})
}

// sortByTimeDesc orders items newest first by the timestamp key returns;
// Trakt timestamps share one layout, so they compare as strings.
func sortByTimeDesc[T any](items []T, key func(T) string) {
	sort.SliceStable(items, func(i, j int) bool { return strings.Compare(key(items[i]), key(items[j])) > 0 })
}
