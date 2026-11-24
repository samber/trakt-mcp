package tools

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/samber/trakt-mcp/internal/trakt"
)

const (
	// maxCheckinMessageRunes bounds a check-in message: Trakt may share it
	// on social networks, where 280 characters is one post.
	maxCheckinMessageRunes = 280
	// maxShowTitleRunes bounds a show title used to find a show: real
	// titles are far shorter.
	maxShowTitleRunes = 200
	// minShowYear is older than any television show; maxShowYearAhead
	// allows shows announced a few years ahead.
	minShowYear      = 1850
	maxShowYearAhead = 5

	fieldShowTitle = "show_title"
	fieldShowYear  = "show_year"
	fieldMessage   = "message"

	// msgCheckinActive answers a check-in refused because another one runs
	// and Trakt's answer did not say until when.
	msgCheckinActive = "a check-in is already in progress; cancel it with cancel_checkin or wait for it to expire, then retry"
)

// ShowCheckinArgs are the arguments of checkin_to_show.
type ShowCheckinArgs struct {
	ShowID    string `json:"show_id,omitempty"    jsonschema:"Trakt slug (breaking-bad), numeric Trakt id or IMDb id; or give show_title and show_year instead"`
	ShowTitle string `json:"show_title,omitempty" jsonschema:"show title, with show_year, when show_id is not known"`
	ShowYear  int    `json:"show_year,omitempty"  jsonschema:"year the show started, with show_title"`
	Season    int    `json:"season"               jsonschema:"season number, 0 to 999; 0 is the specials"`
	Episode   int    `json:"episode"              jsonschema:"episode number in the season, 1 to 9999"`
	Message   string `json:"message,omitempty"    jsonschema:"message shared with the check-in, at most 280 characters"`
}

// MovieCheckinArgs are the arguments of checkin_to_movie.
type MovieCheckinArgs struct {
	MovieID string `json:"movie_id"          jsonschema:"Trakt slug (the-matrix-1999), numeric Trakt id (481) or IMDb id (tt0133093)"`
	Message string `json:"message,omitempty" jsonschema:"message shared with the check-in, at most 280 characters"`
}

// CancelCheckinArgs are the arguments of cancel_checkin: none.
type CancelCheckinArgs struct{}

// CheckinResult is the outcome of a check-in. When another check-in is
// active, CheckedIn is false, AlreadyCheckedIn true, and ExpiresAt and Item
// describe the active one.
type CheckinResult struct {
	CheckedIn        bool      `json:"checked_in"`
	AlreadyCheckedIn bool      `json:"already_checked_in"`
	ID               int64     `json:"id,omitempty"         jsonschema:"check-in id"`
	WatchedAt        string    `json:"watched_at,omitempty"`
	ExpiresAt        string    `json:"expires_at,omitempty" jsonschema:"when the active check-in ends and a new one is possible"`
	Item             *MediaRef `json:"item,omitempty"       jsonschema:"what is checked in"`
}

func (CheckinResult) resultCount() int { return 1 }

// CancelResult confirms a canceled check-in.
type CancelResult struct {
	Canceled bool `json:"canceled"`
}

func (CancelResult) resultCount() int { return 1 }

func registerCheckinTools(s *Server) {
	Register(s, Spec{
		Name:  "checkin_to_show",
		Title: "Check in to an episode",
		Description: "Tell Trakt the user is watching an episode now, by show_id or by show_title and show_year, plus season and episode. " +
			"If a check-in is already active, returns already_checked_in with its expiry instead of failing.",
		Access: Write,
	}, checkinToShow)
	Register(s, Spec{
		Name:  "checkin_to_movie",
		Title: "Check in to a movie",
		Description: "Tell Trakt the user is watching a movie now. " +
			"If a check-in is already active, returns already_checked_in with its expiry instead of failing.",
		Access: Write,
	}, checkinToMovie)
	Register(s, Spec{
		Name:        "cancel_checkin",
		Title:       "Cancel check-in",
		Description: "Cancel the user's active check-in, if any.",
		Access:      Destroy,
	}, cancelCheckin)
}

// checkinRef names a show or movie in a check-in body: by ids, or a show by
// title and year.
type checkinRef struct {
	IDs   *SyncIDs `json:"ids,omitempty"`
	Title string   `json:"title,omitempty"`
	Year  int      `json:"year,omitempty"`
}

type checkinEpisode struct {
	Season int `json:"season"`
	Number int `json:"number"`
}

type checkinBody struct {
	Movie   *checkinRef     `json:"movie,omitempty"`
	Show    *checkinRef     `json:"show,omitempty"`
	Episode *checkinEpisode `json:"episode,omitempty"`
	Message string          `json:"message,omitempty"`
}

func checkMessage(msg string) error {
	if !utf8.ValidString(msg) || utf8.RuneCountInString(msg) > maxCheckinMessageRunes {
		return invalidArgument(fieldMessage)
	}

	return nil
}

func refByID(field, raw string) (*checkinRef, error) {
	id, err := idArg(field, raw, Slug|Numeric|IMDbTitle)
	if err != nil {
		return nil, err
	}

	ids, err := syncIDsOf(id)
	if err != nil {
		return nil, invalidArgument(field)
	}

	return &checkinRef{IDs: &ids}, nil
}

// showRef validates the show of checkin_to_show: show_id alone, or
// show_title with show_year; both at once are ambiguous.
func showRef(in ShowCheckinArgs) (*checkinRef, error) {
	byTitle := in.ShowTitle != "" || in.ShowYear != 0

	switch {
	case in.ShowID != "" && byTitle, in.ShowID == "" && !byTitle:
		return nil, invalidArgument(fieldShowID)
	case in.ShowID != "":
		return refByID(fieldShowID, in.ShowID)
	}

	title := strings.TrimSpace(in.ShowTitle)
	if title == "" || !utf8.ValidString(title) || utf8.RuneCountInString(title) > maxShowTitleRunes {
		return nil, invalidArgument(fieldShowTitle)
	}

	if in.ShowYear < minShowYear || in.ShowYear > time.Now().Year()+maxShowYearAhead {
		return nil, invalidArgument(fieldShowYear)
	}

	return &checkinRef{Title: title, Year: in.ShowYear}, nil
}

func checkinToShow(ctx context.Context, c *trakt.UserClient, in ShowCheckinArgs) (CheckinResult, error) {
	show, err := showRef(in)
	if err != nil {
		return CheckinResult{}, err
	}

	if err := checkSeason(in.Season); err != nil {
		return CheckinResult{}, err
	}

	if err := checkEpisode(in.Episode); err != nil {
		return CheckinResult{}, err
	}

	if err := checkMessage(in.Message); err != nil {
		return CheckinResult{}, err
	}

	return checkin(ctx, c, checkinBody{Show: show, Episode: &checkinEpisode{Season: in.Season, Number: in.Episode}, Message: in.Message})
}

func checkinToMovie(ctx context.Context, c *trakt.UserClient, in MovieCheckinArgs) (CheckinResult, error) {
	movie, err := refByID(fieldMovieID, in.MovieID)
	if err != nil {
		return CheckinResult{}, err
	}

	if err := checkMessage(in.Message); err != nil {
		return CheckinResult{}, err
	}

	return checkin(ctx, c, checkinBody{Movie: movie, Message: in.Message})
}

type traktCheckin struct {
	ID        int64   `json:"id"`
	WatchedAt *string `json:"watched_at"`
	traktMedia
}

// checkin starts a check-in. A 409 means another check-in is active: that
// is an answer for the user, not a failure, so it returns a result.
func checkin(ctx context.Context, c *trakt.UserClient, body checkinBody) (CheckinResult, error) {
	data, err := json.Marshal(body)
	if err != nil {
		return CheckinResult{}, fmt.Errorf("encode check-in body: %w", err)
	}

	r, err := readReply(func() (*http.Response, error) {
		return c.API().PostCheckinStartWithBody(ctx, jsonContentType, bytes.NewReader(data))
	})
	if err != nil {
		return activeCheckin(err)
	}

	raw, err := decodeAs[traktCheckin](r.body)
	if err != nil {
		return CheckinResult{}, err
	}

	return CheckinResult{CheckedIn: true, ID: raw.ID, WatchedAt: deref(raw.WatchedAt), Item: mediaOrNil(raw.trim())}, nil
}

// activeCheckin turns Trakt's 409 into the active check-in: its expiry and,
// when it parses, what is being watched. The body is untrusted Trakt JSON,
// so only validated fields leave this function; a body without a valid
// expiry gives msgCheckinActive. Any other error passes through.
func activeCheckin(err error) (CheckinResult, error) {
	te, ok := errors.AsType[*trakt.Error](err)
	if !ok || te.Kind != trakt.KindConflict {
		return CheckinResult{}, err
	}

	body := te.ConflictBody()

	var head struct {
		ExpiresAt string `json:"expires_at"`
	}
	if json.Unmarshal(body, &head) != nil {
		return CheckinResult{}, newToolError(msgCheckinActive)
	}

	expires, perr := time.Parse(time.RFC3339Nano, head.ExpiresAt)
	if perr != nil {
		return CheckinResult{}, newToolError(msgCheckinActive)
	}

	out := CheckinResult{AlreadyCheckedIn: true, ExpiresAt: expires.UTC().Format(traktTimeLayout)}

	// The item is decoded on its own, so a malformed item still leaves the
	// expiry to report.
	var media traktMedia
	if json.Unmarshal(body, &media) == nil {
		out.Item = mediaOrNil(minimalMedia(media.trim()))
	}

	return out, nil
}

// mediaOrNil returns nil for a MediaRef with nothing in it.
func mediaOrNil(m MediaRef) *MediaRef {
	if m.Movie == nil && m.Show == nil && m.Season == nil && m.Episode == nil {
		return nil
	}

	return &m
}

// validIDOrEmpty keeps s only when it is a valid identifier of kinds.
func validIDOrEmpty(s string, kinds IDKinds) string {
	if _, err := ParseID(s, kinds); err != nil {
		return ""
	}

	return s
}

// minimalMedia keeps only the title, year and validated ids of untrusted
// media, with titles bounded.
func minimalMedia(m MediaRef) MediaRef {
	var out MediaRef

	if m.Movie != nil {
		ids := m.Movie.IDs
		ids.Slug, ids.IMDb = validIDOrEmpty(ids.Slug, Slug), validIDOrEmpty(ids.IMDb, IMDbTitle)
		out.Movie = &Movie{Title: boundText(m.Movie.Title, maxTitleRunes), Year: m.Movie.Year, IDs: ids, Genres: []string{}}
	}

	if m.Show != nil {
		ids := m.Show.IDs
		ids.Slug, ids.IMDb = validIDOrEmpty(ids.Slug, Slug), validIDOrEmpty(ids.IMDb, IMDbTitle)
		out.Show = &Show{Title: boundText(m.Show.Title, maxTitleRunes), Year: m.Show.Year, IDs: ids, Genres: []string{}}
	}

	if m.Episode != nil {
		ids := m.Episode.IDs
		ids.IMDb = validIDOrEmpty(ids.IMDb, IMDbTitle)
		out.Episode = &Episode{Season: m.Episode.Season, Number: m.Episode.Number, Title: m.Episode.Title, IDs: ids}
	}

	return out
}

func cancelCheckin(ctx context.Context, c *trakt.UserClient, _ CancelCheckinArgs) (CancelResult, error) {
	if _, err := readReply(func() (*http.Response, error) { return c.API().DeleteCheckinDelete(ctx) }); err != nil {
		return CancelResult{}, err
	}

	return CancelResult{Canceled: true}, nil
}
