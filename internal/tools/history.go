package tools

import (
	"context"
	"encoding/json"
	"io"
	"math"
	"net/http"
	"time"

	"github.com/samber/trakt-mcp/internal/trakt"
	"github.com/samber/trakt-mcp/internal/trakt/api"
)

// Argument names, as they appear in tool errors.
const (
	fieldHistoryType  = "history_type"
	fieldItemID       = "item_id"
	fieldStartAt      = "start_at"
	fieldEndAt        = "end_at"
	fieldPlaybackType = "playback_type"
	fieldPlaybackID   = "playback_id"
	fieldLastActivity = "last_activity"
)

const (
	// extendedNoSeasons drops the per-season, per-episode detail of watched
	// shows, which for a long history is far larger than the answer needs.
	extendedNoSeasons = "noseasons"
	// dateLayout is a calendar date, the short form start_at and end_at
	// accept; it is expanded to a timestamp before Trakt sees it.
	dateLayout = "2006-01-02"
	// lastMillisecondOfDay moves a date-only end_at to the end of its day, the
	// finest step traktTimeLayout shows.
	lastMillisecondOfDay = 24*time.Hour - time.Millisecond
	// lastActivityAired and lastActivityWatched are the ways Trakt picks
	// the next episode of a show's progress.
	lastActivityAired   = "aired"
	lastActivityWatched = "watched"
	// queryLastActivity is Trakt's query parameter for last_activity, which
	// the generated client does not know.
	queryLastActivity = "last_activity"
	// percentScale turns a ratio into a percentage; percentPrecision keeps
	// one decimal, enough to tell 99.9 from done.
	percentScale     = 100
	percentPrecision = 10
	// maxPlaybackID is the largest id the generated client's int path
	// parameter holds on this platform.
	maxPlaybackID = math.MaxInt
)

var (
	watchedTypes  = []string{mediaMovies, mediaShows}
	historyTypes  = []string{mediaMovies, mediaShows, mediaSeasons, mediaEpisodes}
	playbackTypes = []string{mediaMovies, mediaEpisodes}
	lastActivity  = []string{lastActivityAired, lastActivityWatched}
)

// UserWatchedArgs are the arguments of list_user_watched.
type UserWatchedArgs struct {
	Type string `json:"type" jsonschema:"plural: movies or shows"`
	PageArgs
}

// WatchedItem is a movie or show the user watched, newest first.
type WatchedItem struct {
	Plays         int    `json:"plays"`
	LastWatchedAt string `json:"last_watched_at,omitempty"`
	LastUpdatedAt string `json:"last_updated_at,omitempty"`
	MediaRef
}

// HistoryArgs are the arguments of list_history.
type HistoryArgs struct {
	HistoryType string `json:"history_type,omitempty" jsonschema:"movies, shows, seasons or episodes; omit for every type"`
	ItemID      string `json:"item_id,omitempty"      jsonschema:"numeric Trakt id of one movie, show, season or episode to list only its plays; requires history_type"`
	StartAt     string `json:"start_at,omitempty"     jsonschema:"only plays at or after this time: ISO 8601 (2024-01-31T20:00:00.000Z) or a date (2024-01-31, from 00:00 UTC)"`
	EndAt       string `json:"end_at,omitempty"       jsonschema:"only plays at or before this time: ISO 8601 or a date (whole day included, until 23:59:59.999 UTC)"`
	PageArgs
}

// HistoryEntry is one play in the user's history, newest first.
type HistoryEntry struct {
	ID        int64  `json:"id"         jsonschema:"history entry id"`
	WatchedAt string `json:"watched_at"`
	Action    string `json:"action"     jsonschema:"scrobble, checkin or watch"`
	Type      string `json:"type"       jsonschema:"movie or episode"`
	MediaRef
}

// AddHistoryArgs are the arguments of add_to_history.
type AddHistoryArgs struct {
	Items     []ItemArg `json:"items"                jsonschema:"1 to 50 items; a show item marks every aired episode, a season item every episode of that season"`
	WatchedAt string    `json:"watched_at,omitempty" jsonschema:"when: ISO 8601 (2024-01-31T20:00:00.000Z), unknown, or released (episodes only: the air date); default now"`
}

// ShowProgressArgs are the arguments of get_show_progress.
type ShowProgressArgs struct {
	ShowID        string `json:"id" jsonschema:"Trakt slug (breaking-bad), numeric Trakt id (1388) or IMDb id (tt0903747)"`
	Hidden        *bool  `json:"hidden,omitempty"         jsonschema:"include seasons the user hid (default false)"`
	Specials      *bool  `json:"specials,omitempty"       jsonschema:"include specials as season 0 (default false)"`
	CountSpecials *bool  `json:"count_specials,omitempty" jsonschema:"count specials in the totals when included (default true)"`
	LastActivity  string `json:"last_activity,omitempty"  jsonschema:"aired or watched: whether the next episode follows the last aired or the last watched episode"`
	Verbose       bool   `json:"verbose,omitempty"        jsonschema:"list every episode with its watched state and date (default false: per-season counts only)"`
}

// ShowProgress is the user's watch progress on one show.
type ShowProgress struct {
	Aired         int              `json:"aired"`
	Completed     int              `json:"completed"`
	Percentage    float64          `json:"percentage"            jsonschema:"completed out of aired, in percent"`
	LastWatchedAt string           `json:"last_watched_at,omitempty"`
	ResetAt       string           `json:"reset_at,omitempty"    jsonschema:"when the user restarted the show"`
	NextEpisode   *Episode         `json:"next_episode,omitempty"`
	LastEpisode   *Episode         `json:"last_episode,omitempty"`
	Seasons       []SeasonProgress `json:"seasons"`
	HiddenSeasons []int            `json:"hidden_seasons"        jsonschema:"numbers of the seasons the user hid"`
}

func (ShowProgress) resultCount() int { return 1 }

// SeasonProgress is the progress on one season. Episodes is present only
// when verbose was asked.
type SeasonProgress struct {
	Number     int                `json:"number"`
	Title      string             `json:"title,omitempty"`
	Aired      int                `json:"aired"`
	Completed  int                `json:"completed"`
	Percentage float64            `json:"percentage"`
	Episodes   *[]EpisodeProgress `json:"episodes,omitempty"`
}

// EpisodeProgress is the watched state of one episode.
type EpisodeProgress struct {
	Number        int    `json:"number"`
	Completed     bool   `json:"completed"`
	LastWatchedAt string `json:"last_watched_at,omitempty"`
}

// PlaybackArgs are the arguments of list_playback_progress.
type PlaybackArgs struct {
	PlaybackType string `json:"playback_type,omitempty" jsonschema:"movies or episodes; omit for both"`
	PageArgs
}

// PlaybackItem is a paused movie or episode.
type PlaybackItem struct {
	PlaybackID int64   `json:"playback_id" jsonschema:"id for remove_playback_item"`
	Progress   float64 `json:"progress"    jsonschema:"percent watched, 0 to 100"`
	PausedAt   string  `json:"paused_at,omitempty"`
	Type       string  `json:"type"        jsonschema:"movie or episode"`
	MediaRef
}

// RemovePlaybackArgs are the arguments of remove_playback_item.
type RemovePlaybackArgs struct {
	PlaybackID int64 `json:"playback_id" jsonschema:"playback_id from list_playback_progress"`
}

// RemovedPlayback confirms a removed playback item.
type RemovedPlayback struct {
	Removed    bool  `json:"removed"`
	PlaybackID int64 `json:"playback_id"`
}

func (RemovedPlayback) resultCount() int { return 1 }

func registerHistoryTools(s *Server) {
	Register(s, Spec{
		Name:        "list_user_watched",
		Title:       "List watched movies or shows",
		Description: "List every movie or show the user has watched, most recently watched first, with play count and last watch time. This is the user's own watched history; Trakt-wide charts are in list_watched.",
		Access:      Read,
	}, fetchUserWatched)
	Register(s, Spec{
		Name:  "list_history",
		Title: "List watch history",
		Description: "List the user's plays, newest first, optionally of one type or one item and within a date range. " +
			"Each entry has a history id, the time and how it was recorded.",
		Access: Read,
	}, fetchHistory)
	Register(s, Spec{
		Name:  "add_to_history",
		Title: "Add to watch history",
		Description: "Mark up to 50 movies, shows, seasons or episodes as watched, now or at watched_at. " +
			"Returns how many plays were added and which items Trakt did not find.",
		Access: Write,
	}, addToHistory)
	Register(s, Spec{
		Name:  "remove_from_history",
		Title: "Remove from watch history",
		Description: "Remove every play of up to 50 movies, shows, seasons or episodes from the user's history. " +
			"Returns how many plays were deleted and which items Trakt did not find.",
		Access: Destroy,
	}, removeFromHistory)
	Register(s, Spec{
		Name:  "get_show_progress",
		Title: "Get show progress",
		Description: "Get the user's progress on one TV show: episodes aired and watched, percentage, next episode to watch, " +
			"and per-season counts (every episode with verbose).",
		Access: Read,
	}, fetchShowProgress)
	Register(s, Spec{
		Name:        "list_playback_progress",
		Title:       "List paused playback",
		Description: "List movies and episodes the user paused mid-way, with percent watched and the playback_id remove_playback_item takes.",
		Access:      Read,
	}, fetchPlaybackProgress)
	Register(s, Spec{
		Name:        "remove_playback_item",
		Title:       "Remove paused playback",
		Description: "Remove one paused playback item, so it no longer shows as in progress.",
		Access:      Destroy,
	}, removePlaybackItem)
}

type traktWatched struct {
	Plays         int     `json:"plays"`
	LastWatchedAt *string `json:"last_watched_at"`
	LastUpdatedAt *string `json:"last_updated_at"`
	traktMedia
}

func trimWatched(w traktWatched) WatchedItem {
	return WatchedItem{Plays: w.Plays, LastWatchedAt: deref(w.LastWatchedAt), LastUpdatedAt: deref(w.LastUpdatedAt), MediaRef: w.trim()}
}

func fetchUserWatched(ctx context.Context, c *trakt.UserClient, in UserWatchedArgs) (Listing[WatchedItem], error) {
	typ, err := oneOf(fieldType, in.Type, "", watchedTypes)
	if err != nil || typ == "" {
		return Listing[WatchedItem]{}, invalidArgument(fieldType)
	}

	params := &api.GetSyncWatchedParams{}

	if typ == mediaShows {
		e := extendedNoSeasons
		params.Extended = &e
	}

	r, err := readReply(func() (*http.Response, error) { return c.API().GetSyncWatched(ctx, typ, params) })
	if err != nil {
		return Listing[WatchedItem]{}, err
	}

	items, err := trimAll(r.body, trimWatched)
	if err != nil {
		return Listing[WatchedItem]{}, err
	}

	// Trakt returns the whole list unpaginated; the newest plays answer most
	// questions, so the tool sorts and pages it itself.
	sortByTimeDesc(items, func(w WatchedItem) string { return w.LastWatchedAt })

	return localPage(ctx, in.PageArgs, items)
}

type traktHistory struct {
	ID        int64  `json:"id"`
	WatchedAt string `json:"watched_at"`
	Action    string `json:"action"`
	Type      string `json:"type"`
	traktMedia
}

func trimHistory(h traktHistory) HistoryEntry {
	return HistoryEntry{ID: h.ID, WatchedAt: h.WatchedAt, Action: h.Action, Type: h.Type, MediaRef: h.trim()}
}

// historyRange is a validated start_at and end_at, nil when omitted.
type historyRange struct{ start, end *string }

// rangeBound validates one bound: a calendar date or an ISO 8601 timestamp,
// sent in traktTimeLayout. A date is read in UTC as the start of that day, or
// with endOfDay as its last millisecond, so "start_at=end_at=2024-01-31"
// covers the whole day instead of the single instant at midnight.
func rangeBound(field, value string, endOfDay bool) (*string, time.Time, error) {
	if value == "" {
		return nil, time.Time{}, nil
	}

	t, err := time.Parse(dateLayout, value)
	if err == nil {
		if endOfDay {
			t = t.Add(lastMillisecondOfDay)
		}
	} else if t, err = time.Parse(time.RFC3339Nano, value); err != nil {
		return nil, time.Time{}, invalidArgument(field)
	}

	s := t.UTC().Format(traktTimeLayout)

	return &s, t, nil
}

func parseRange(startAt, endAt string) (historyRange, error) {
	start, startT, err := rangeBound(fieldStartAt, startAt, false)
	if err != nil {
		return historyRange{}, err
	}

	end, endT, err := rangeBound(fieldEndAt, endAt, true)
	if err != nil {
		return historyRange{}, err
	}

	if start != nil && end != nil && endT.Before(startT) {
		return historyRange{}, invalidArgument(fieldEndAt)
	}

	return historyRange{start: start, end: end}, nil
}

func fetchHistory(ctx context.Context, c *trakt.UserClient, in HistoryArgs) (Listing[HistoryEntry], error) {
	typ, err := oneOf(fieldHistoryType, in.HistoryType, "", historyTypes)
	if err != nil {
		return Listing[HistoryEntry]{}, err
	}

	var itemID ID

	if in.ItemID != "" {
		if typ == "" {
			return Listing[HistoryEntry]{}, invalidArgument(fieldItemID)
		}

		if itemID, err = idArg(fieldItemID, in.ItemID, Numeric); err != nil {
			return Listing[HistoryEntry]{}, err
		}
	}

	rng, err := parseRange(in.StartAt, in.EndAt)
	if err != nil {
		return Listing[HistoryEntry]{}, err
	}

	return paginateDecoded(ctx, in.PageArgs, trimHistory, func(page, limit int) (*http.Response, error) {
		switch {
		case typ == "":
			return historyAll(ctx, c, rng, page, limit)
		case itemID == "":
			return historyByType(ctx, c, typ, rng, page, limit)
		}

		return historyByItem(ctx, c, typ, itemID, rng, page, limit)
	})
}

// historyAll is GET /sync/history, Trakt's form without a type; whether
// Trakt answers it is confirmed only against the live API.
func historyAll(ctx context.Context, c *trakt.UserClient, rng historyRange, page, limit int) (*http.Response, error) {
	//nolint:wrapcheck // *trakt.Error is mapped by Kind
	return c.API().GetSyncHistoryAll(ctx, &api.GetSyncHistoryAllParams{Page: &page, Limit: &limit, StartAt: rng.start, EndAt: rng.end})
}

// historyByType is GET /sync/history/{type}.
func historyByType(ctx context.Context, c *trakt.UserClient, typ string, rng historyRange, page, limit int) (*http.Response, error) {
	//nolint:wrapcheck // *trakt.Error is mapped by Kind
	return c.API().GetSyncHistoryByType(ctx, typ, &api.GetSyncHistoryByTypeParams{Page: &page, Limit: &limit, StartAt: rng.start, EndAt: rng.end})
}

// historyByItem is GET /sync/history/{type}/{id}, the full form.
func historyByItem(ctx context.Context, c *trakt.UserClient, typ string, id ID, rng historyRange, page, limit int) (*http.Response, error) {
	//nolint:wrapcheck // *trakt.Error is mapped by Kind
	return c.API().GetSyncHistoryGet(ctx, typ, string(id), &api.GetSyncHistoryGetParams{Page: &page, Limit: &limit, StartAt: rng.start, EndAt: rng.end})
}

func addToHistory(ctx context.Context, c *trakt.UserClient, in AddHistoryArgs) (WriteResult, error) {
	items, err := parseItems(in.Items)
	if err != nil {
		return WriteResult{}, err
	}

	watchedAt, err := watchedAtArg(in.WatchedAt, items)
	if err != nil {
		return WriteResult{}, err
	}

	for i := range items {
		items[i].WatchedAt = watchedAt
	}

	return writeItems(items, func(body io.Reader) (*http.Response, error) {
		return c.API().PostSyncHistoryAddWithBody(ctx, jsonContentType, body)
	})
}

func removeFromHistory(ctx context.Context, c *trakt.UserClient, in ItemsArgs) (WriteResult, error) {
	items, err := parseItems(in.Items)
	if err != nil {
		return WriteResult{}, err
	}

	return writeItems(items, func(body io.Reader) (*http.Response, error) {
		return c.API().PostSyncHistoryRemoveWithBody(ctx, jsonContentType, body)
	})
}

type traktShowProgress struct {
	Aired         int             `json:"aired"`
	Completed     int             `json:"completed"`
	LastWatchedAt *string         `json:"last_watched_at"`
	ResetAt       *string         `json:"reset_at"`
	NextEpisode   json.RawMessage `json:"next_episode"`
	LastEpisode   json.RawMessage `json:"last_episode"`
	Seasons       []struct {
		Number    int     `json:"number"`
		Title     *string `json:"title"`
		Aired     int     `json:"aired"`
		Completed int     `json:"completed"`
		Episodes  []struct {
			Number        int     `json:"number"`
			Completed     bool    `json:"completed"`
			LastWatchedAt *string `json:"last_watched_at"`
		} `json:"episodes"`
	} `json:"seasons"`
	HiddenSeasons []struct {
		Number int `json:"number"`
	} `json:"hidden_seasons"`
}

// percentage is done out of total in percent with one decimal, 0 when
// nothing aired.
func percentage(done, total int) float64 {
	if total <= 0 {
		return 0
	}

	return math.Round(float64(done)*percentScale*percentPrecision/float64(total)) / percentPrecision
}

// optionalEpisode decodes next_episode or last_episode, which Trakt sends
// as an object, null, or (per its schema) a string; anything but an
// episode object gives nil.
func optionalEpisode(raw json.RawMessage) *Episode {
	var e *traktEpisode
	if len(raw) == 0 || json.Unmarshal(raw, &e) != nil || e == nil {
		return nil
	}

	out := e.trim(maxListOverviewRunes)

	return &out
}

// withQuery adds a query parameter the generated client does not know.
func withQuery(key, value string) api.RequestEditorFn {
	return func(_ context.Context, r *http.Request) error {
		q := r.URL.Query()
		q.Set(key, value)
		r.URL.RawQuery = q.Encode()

		return nil
	}
}

func fetchShowProgress(ctx context.Context, c *trakt.UserClient, in ShowProgressArgs) (ShowProgress, error) {
	id, err := showPath(in.ShowID)
	if err != nil {
		return ShowProgress{}, err
	}

	activity, err := oneOf(fieldLastActivity, in.LastActivity, "", lastActivity)
	if err != nil {
		return ShowProgress{}, err
	}

	var editors []api.RequestEditorFn
	if activity != "" {
		editors = append(editors, withQuery(queryLastActivity, activity))
	}

	params := &api.GetShowsProgressWatchedParams{Hidden: in.Hidden, Specials: in.Specials, CountSpecials: in.CountSpecials}

	r, err := readReply(func() (*http.Response, error) { return c.API().GetShowsProgressWatched(ctx, id, params, editors...) })
	if err != nil {
		return ShowProgress{}, err
	}

	raw, err := decodeAs[traktShowProgress](r.body)
	if err != nil {
		return ShowProgress{}, err
	}

	return raw.trim(in.Verbose), nil
}

func (p traktShowProgress) trim(verbose bool) ShowProgress {
	out := ShowProgress{
		Aired:         p.Aired,
		Completed:     p.Completed,
		Percentage:    percentage(p.Completed, p.Aired),
		LastWatchedAt: deref(p.LastWatchedAt),
		ResetAt:       deref(p.ResetAt),
		NextEpisode:   optionalEpisode(p.NextEpisode),
		LastEpisode:   optionalEpisode(p.LastEpisode),
		Seasons:       make([]SeasonProgress, 0, len(p.Seasons)),
		HiddenSeasons: make([]int, 0, len(p.HiddenSeasons)),
	}

	for _, s := range p.Seasons {
		sp := SeasonProgress{
			Number:     s.Number,
			Title:      boundText(deref(s.Title), maxTitleRunes),
			Aired:      s.Aired,
			Completed:  s.Completed,
			Percentage: percentage(s.Completed, s.Aired),
		}

		if verbose {
			eps := make([]EpisodeProgress, 0, len(s.Episodes))
			for _, e := range s.Episodes {
				eps = append(eps, EpisodeProgress{Number: e.Number, Completed: e.Completed, LastWatchedAt: deref(e.LastWatchedAt)})
			}

			sp.Episodes = &eps
		}

		out.Seasons = append(out.Seasons, sp)
	}

	for _, h := range p.HiddenSeasons {
		out.HiddenSeasons = append(out.HiddenSeasons, h.Number)
	}

	return out
}

type traktPlayback struct {
	ID       int64   `json:"id"`
	Progress float64 `json:"progress"`
	PausedAt *string `json:"paused_at"`
	Type     string  `json:"type"`
	traktMedia
}

func trimPlayback(p traktPlayback) PlaybackItem {
	return PlaybackItem{PlaybackID: p.ID, Progress: p.Progress, PausedAt: deref(p.PausedAt), Type: p.Type, MediaRef: p.trim()}
}

func fetchPlaybackProgress(ctx context.Context, c *trakt.UserClient, in PlaybackArgs) (Listing[PlaybackItem], error) {
	typ, err := oneOf(fieldPlaybackType, in.PlaybackType, "", playbackTypes)
	if err != nil {
		return Listing[PlaybackItem]{}, err
	}

	return paginateDecoded(ctx, in.PageArgs, trimPlayback, func(page, limit int) (*http.Response, error) {
		switch typ {
		case mediaMovies:
			return c.API().GetSyncProgressMovies(ctx, &api.GetSyncProgressMoviesParams{Page: &page, Limit: &limit})
		case mediaEpisodes:
			return c.API().GetSyncProgressEpisodes(ctx, &api.GetSyncProgressEpisodesParams{Page: &page, Limit: &limit})
		}

		return c.API().GetSyncProgressPlayback(ctx, &api.GetSyncProgressPlaybackParams{Page: &page, Limit: &limit})
	})
}

func removePlaybackItem(ctx context.Context, c *trakt.UserClient, in RemovePlaybackArgs) (RemovedPlayback, error) {
	if in.PlaybackID <= 0 || in.PlaybackID > maxPlaybackID {
		return RemovedPlayback{}, invalidArgument(fieldPlaybackID)
	}

	if _, err := readReply(func() (*http.Response, error) {
		return c.API().DeleteSyncPlaybackItem(ctx, int(in.PlaybackID))
	}); err != nil {
		return RemovedPlayback{}, err
	}

	return RemovedPlayback{Removed: true, PlaybackID: in.PlaybackID}, nil
}
