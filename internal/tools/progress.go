package tools

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"time"

	"github.com/samber/trakt-mcp/internal/trakt"
	"github.com/samber/trakt-mcp/internal/trakt/api"
)

const (
	// fieldResetAt names the reset_at argument in tool errors.
	fieldResetAt = "reset_at"
	// lastActivityCollected is the collection-progress alternative to
	// watched: next and last episode follow the last collected episode.
	lastActivityCollected = "collected"

	// maxResetFutureSkew is how far ahead of this server's clock a reset_at may
	// be: Trakt refuses a reset that starts later than now, so only a few
	// minutes of clock drift between caller and server are tolerated.
	maxResetFutureSkew = 5 * time.Minute

	// resetVIPNote tells the assistant what a reset is and that it is paid.
	resetVIPNote = "Applies to TV shows only. The user's watch history is kept: a reset starts a rewatch, " +
		"and progress calculations then ignore earlier plays. Trakt VIP only."
)

// collectionActivity is the values Trakt accepts for last_activity on
// collection progress.
var collectionActivity = []string{lastActivityAired, lastActivityCollected}

// CollectionProgressArgs are the arguments of get_show_collection_progress.
type CollectionProgressArgs struct {
	ShowID        string `json:"id" jsonschema:"Trakt slug (breaking-bad), numeric Trakt id (1388) or IMDb id (tt0903747)"`
	Hidden        *bool  `json:"hidden,omitempty"         jsonschema:"include seasons the user hid (default false)"`
	Specials      *bool  `json:"specials,omitempty"       jsonschema:"include specials as season 0 (default false)"`
	CountSpecials *bool  `json:"count_specials,omitempty" jsonschema:"count specials in the totals when included (default true)"`
	LastActivity  string `json:"last_activity,omitempty"  jsonschema:"aired or collected: whether the next episode follows the last aired or the last collected episode"`
	Verbose       bool   `json:"verbose,omitempty"        jsonschema:"list every episode with its collected state and date (default false: per-season counts only)"`
}

// CollectionProgress is how much of one show the user has collected.
type CollectionProgress struct {
	Aired           int                        `json:"aired"`
	Completed       int                        `json:"completed"                   jsonschema:"aired episodes the user collected"`
	Percentage      float64                    `json:"percentage"                  jsonschema:"completed out of aired, in percent"`
	LastCollectedAt string                     `json:"last_collected_at,omitempty"`
	NextEpisode     *Episode                   `json:"next_episode,omitempty"`
	LastEpisode     *Episode                   `json:"last_episode,omitempty"`
	Seasons         []CollectionSeasonProgress `json:"seasons"`
	HiddenSeasons   []int                      `json:"hidden_seasons"              jsonschema:"numbers of the seasons the user hid"`
}

func (CollectionProgress) resultCount() int { return 1 }

// CollectionSeasonProgress is the collection progress on one season.
// Episodes is present only when verbose was asked.
type CollectionSeasonProgress struct {
	Number     int                          `json:"number"`
	Title      string                       `json:"title,omitempty"`
	Aired      int                          `json:"aired"`
	Completed  int                          `json:"completed"`
	Percentage float64                      `json:"percentage"`
	Episodes   *[]CollectionEpisodeProgress `json:"episodes,omitempty"`
}

// CollectionEpisodeProgress is the collected state of one episode.
type CollectionEpisodeProgress struct {
	Number      int    `json:"number"`
	Collected   bool   `json:"collected"`
	CollectedAt string `json:"collected_at,omitempty"`
}

// ResetProgressArgs are the arguments of reset_show_progress.
type ResetProgressArgs struct {
	ShowID  string `json:"id" jsonschema:"Trakt slug (breaking-bad), numeric Trakt id (1388) or IMDb id (tt0903747)"`
	ResetAt string `json:"reset_at,omitempty" jsonschema:"RFC 3339 timestamp (2024-01-31T20:00:00Z) the rewatch starts from, not in the future; omit for now"`
}

// ResetProgressResult confirms a reset.
type ResetProgressResult struct {
	Reset   bool   `json:"reset"`
	ResetAt string `json:"reset_at,omitempty" jsonschema:"when Trakt says the rewatch starts"`
}

func (ResetProgressResult) resultCount() int { return 1 }

// UndoResetResult confirms an undone reset.
type UndoResetResult struct {
	Undone bool `json:"undone"`
}

func (UndoResetResult) resultCount() int { return 1 }

type resetProgressBody struct {
	ResetAt string `json:"reset_at"`
}

type traktResetResult struct {
	ResetAt *string `json:"reset_at"`
}

type traktCollectionProgress struct {
	Aired           int                     `json:"aired"`
	Completed       int                     `json:"completed"`
	LastCollectedAt *string                 `json:"last_collected_at"`
	NextEpisode     json.RawMessage         `json:"next_episode"`
	LastEpisode     json.RawMessage         `json:"last_episode"`
	Seasons         []traktCollectionSeason `json:"seasons"`
	HiddenSeasons   []struct {
		Number int `json:"number"`
	} `json:"hidden_seasons"`
}

type traktCollectionSeason struct {
	Number    int     `json:"number"`
	Title     *string `json:"title"`
	Aired     int     `json:"aired"`
	Completed int     `json:"completed"`
	Episodes  []struct {
		Number      int     `json:"number"`
		Completed   bool    `json:"completed"`
		CollectedAt *string `json:"collected_at"`
	} `json:"episodes"`
}

// registerProgressTools registers the show collection progress and reset tools.
func registerProgressTools(s *Server) {
	Register(s, Spec{
		Name: "get_show_collection_progress", Title: "Get TV show collection progress", Access: Read,
		Description: "Get how much of one TV show the user has collected: episodes aired and collected, percentage, " +
			"next episode to collect, and per-season counts (every episode with verbose).",
	}, fetchShowCollectionProgress)
	Register(s, Spec{
		Name: "reset_show_progress", Title: "Reset TV show progress", Access: Write,
		Description: "Restart a TV show's watch progress from now or from reset_at, to rewatch it. " + resetVIPNote +
			" Reverse it with undo_reset_show_progress.",
	}, resetShowProgress)
	Register(s, Spec{
		Name: "undo_reset_show_progress", Title: "Undo TV show progress reset", Access: Write,
		Description: "Undo the last reset of a TV show's watch progress, so earlier plays count again. " + resetVIPNote,
	}, undoResetShowProgress)
}

func fetchShowCollectionProgress(ctx context.Context, c *trakt.UserClient, in CollectionProgressArgs) (CollectionProgress, error) {
	id, err := showPath(in.ShowID)
	if err != nil {
		return CollectionProgress{}, err
	}

	activity, err := oneOf(fieldLastActivity, in.LastActivity, "", collectionActivity)
	if err != nil {
		return CollectionProgress{}, err
	}

	params := &api.GetShowsProgressCollectionParams{Hidden: in.Hidden, Specials: in.Specials, CountSpecials: in.CountSpecials}

	if activity != "" {
		a := api.GetShowsProgressCollectionParamsLastActivity(activity)
		params.LastActivity = &a
	}

	r, err := readReply(func() (*http.Response, error) { return c.API().GetShowsProgressCollection(ctx, id, params) })
	if err != nil {
		return CollectionProgress{}, err
	}

	raw, err := decodeAs[traktCollectionProgress](r.body)
	if err != nil {
		return CollectionProgress{}, err
	}

	return raw.trim(in.Verbose), nil
}

func (p traktCollectionProgress) trim(verbose bool) CollectionProgress {
	out := CollectionProgress{
		Aired:           p.Aired,
		Completed:       p.Completed,
		Percentage:      percentage(p.Completed, p.Aired),
		LastCollectedAt: deref(p.LastCollectedAt),
		NextEpisode:     optionalEpisode(p.NextEpisode),
		LastEpisode:     optionalEpisode(p.LastEpisode),
		Seasons:         make([]CollectionSeasonProgress, 0, len(p.Seasons)),
		HiddenSeasons:   make([]int, 0, len(p.HiddenSeasons)),
	}

	for _, s := range p.Seasons {
		out.Seasons = append(out.Seasons, s.trim(verbose))
	}

	for _, h := range p.HiddenSeasons {
		out.HiddenSeasons = append(out.HiddenSeasons, h.Number)
	}

	return out
}

func (s traktCollectionSeason) trim(verbose bool) CollectionSeasonProgress {
	out := CollectionSeasonProgress{
		Number:     s.Number,
		Title:      boundText(deref(s.Title), maxTitleRunes),
		Aired:      s.Aired,
		Completed:  s.Completed,
		Percentage: percentage(s.Completed, s.Aired),
	}

	if !verbose {
		return out
	}

	eps := make([]CollectionEpisodeProgress, 0, len(s.Episodes))
	for _, e := range s.Episodes {
		eps = append(eps, CollectionEpisodeProgress{Number: e.Number, Collected: e.Completed, CollectedAt: deref(e.CollectedAt)})
	}

	out.Episodes = &eps

	return out
}

func resetShowProgress(ctx context.Context, c *trakt.UserClient, in ResetProgressArgs) (ResetProgressResult, error) {
	id, err := showPath(in.ShowID)
	if err != nil {
		return ResetProgressResult{}, err
	}

	var r reply

	if in.ResetAt == "" {
		// No body: Trakt then starts the rewatch now.
		r, err = readReply(func() (*http.Response, error) {
			return c.API().PostShowsProgressResetWithBody(ctx, id, jsonContentType, http.NoBody)
		})
	} else {
		var at string

		at, err = parseTimestampWithin(fieldResetAt, in.ResetAt, maxResetFutureSkew)
		if err != nil {
			return ResetProgressResult{}, err
		}

		r, err = sendJSON(resetProgressBody{ResetAt: at}, func(b io.Reader) (*http.Response, error) {
			return c.API().PostShowsProgressResetWithBody(ctx, id, jsonContentType, b)
		})
	}

	if err != nil {
		return ResetProgressResult{}, err
	}

	// Trakt documents a 201 with the effective reset_at; an empty answer
	// still means the reset happened.
	if len(bytes.TrimSpace(r.body)) == 0 {
		return ResetProgressResult{Reset: true}, nil
	}

	raw, err := decodeAs[traktResetResult](r.body)
	if err != nil {
		return ResetProgressResult{}, err
	}

	return ResetProgressResult{Reset: true, ResetAt: deref(raw.ResetAt)}, nil
}

func undoResetShowProgress(ctx context.Context, c *trakt.UserClient, in ShowRef) (UndoResetResult, error) {
	id, err := showPath(in.ShowID)
	if err != nil {
		return UndoResetResult{}, err
	}

	if _, err := readReply(func() (*http.Response, error) { return c.API().DeleteShowsProgressUndoReset(ctx, id) }); err != nil {
		return UndoResetResult{}, err
	}

	return UndoResetResult{Undone: true}, nil
}
