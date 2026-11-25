package tools

import (
	"context"
	"io"
	"net/http"
	"strconv"

	"github.com/samber/trakt-mcp/internal/trakt"
	"github.com/samber/trakt-mcp/internal/trakt/api"
)

// Argument names of the note tools, as they appear in tool errors.
const (
	fieldNoteID = "note_id"
	fieldItem   = "item"
)

const (
	// maxNoteRunes bounds the text of a note. UNVERIFIED: the filtered spec
	// states no limit; this reuses the 500-character bound of watchlist
	// notes so a mistaken call stays small.
	maxNoteRunes = maxNotesRunes
	// vipNoteNote tells the assistant that free Trakt accounts have a note cap.
	vipNoteNote = " Free Trakt accounts can keep only a few notes; past the cap Trakt answers with an account limit error."
)

// notePrivacies are the visibility values of a note (spec: POST /notes).
var notePrivacies = []string{"private", "friends", "public"}

// noteItemTypes are the kinds of item a note can be attached to; a note on a
// season or episode needs that season's or episode's own Trakt id.
var noteItemTypes = []string{itemMovie, itemShow, itemSeason, itemEpisode, itemPerson}

// NoteItemArg names the one item a new note is attached to. Trakt takes only
// its numeric Trakt id here, unlike the batch tools.
type NoteItemArg struct {
	Type string `json:"type" jsonschema:"movie, show, season, episode or person"`
	ID   string `json:"id"   jsonschema:"numeric Trakt id of the item (slugs and IMDb ids are not accepted for notes)"`
}

// NoteRefArgs name one note of the connected user.
type NoteRefArgs struct {
	NoteID int `json:"note_id" jsonschema:"id of a note, from list_user_notes or create_note"`
}

// UserNotesArgs are the arguments of list_user_notes.
type UserNotesArgs struct {
	Type string `json:"type,omitempty" jsonschema:"all, movies, shows, seasons, episodes or people (default all)"`
	PageArgs
}

// CreateNoteArgs are the arguments of create_note.
type CreateNoteArgs struct {
	Item    NoteItemArg `json:"item"              jsonschema:"the movie, TV show, season, episode or person the note is about"`
	Notes   string      `json:"notes"             jsonschema:"the note text, 1 to 500 characters"`
	Privacy string      `json:"privacy,omitempty" jsonschema:"private, friends or public (Trakt's default applies when omitted)"`
	Spoiler *bool       `json:"spoiler,omitempty" jsonschema:"mark the note as a spoiler"`
}

// UpdateNoteArgs are the arguments of update_note; a field left out stays as
// it is, and at least one must be given.
type UpdateNoteArgs struct {
	NoteRefArgs
	Notes   *string `json:"notes,omitempty"   jsonschema:"new note text, 1 to 500 characters"`
	Privacy string  `json:"privacy,omitempty" jsonschema:"private, friends or public"`
	Spoiler *bool   `json:"spoiler,omitempty" jsonschema:"mark the note as a spoiler or not"`
}

// NoteDetail is a note as Trakt stores it. Notes is the user's own text.
type NoteDetail struct {
	ID        int64  `json:"id"`
	Notes     string `json:"notes,omitempty"`
	Privacy   string `json:"privacy,omitempty" jsonschema:"private, friends or public"`
	Spoiler   bool   `json:"spoiler,omitempty"`
	CreatedAt string `json:"created_at,omitempty"`
	UpdatedAt string `json:"updated_at,omitempty"`
}

// Note is one note with the type of item it is attached to and, when Trakt
// sends it, that item.
type Note struct {
	NoteDetail
	ItemType string `json:"item_type,omitempty" jsonschema:"movie, show, season, episode or person"`
	MediaRef
	Person *Person `json:"person,omitempty"`
}

func (Note) resultCount() int { return 1 }

// NoteAttachment says which history, collection or rating entry a note is
// attached to, instead of the title itself.
type NoteAttachment struct {
	Type        string `json:"type"                   jsonschema:"history, collection or rating"`
	ID          int64  `json:"id,omitempty"`
	WatchedAt   string `json:"watched_at,omitempty"`
	CollectedAt string `json:"collected_at,omitempty"`
	RatedAt     string `json:"rated_at,omitempty"`
	Rating      int    `json:"rating,omitempty"`
}

// UserNote is an entry of list_user_notes: a note, what it is attached to
// and the media object it belongs to.
type UserNote struct {
	Type       string          `json:"type"                  jsonschema:"movie, show, season, episode or person"`
	AttachedTo *NoteAttachment `json:"attached_to,omitempty"`
	Note       NoteDetail      `json:"note"`
	MediaRef
	Person *Person `json:"person,omitempty"`
}

// DeleteNoteResult confirms a deleted note.
type DeleteNoteResult struct {
	Deleted bool `json:"deleted"`
}

func (DeleteNoteResult) resultCount() int { return 1 }

// noteMedia is the media object of a create body: Trakt matches it by its
// Trakt id alone.
type noteMedia struct {
	IDs struct {
		Trakt int64 `json:"trakt"`
	} `json:"ids"`
}

// createNoteBody is the POST /notes body. Pointers and omitempty keep every
// media key but the chosen one, and every option the caller left out, off the
// wire.
type createNoteBody struct {
	Movie   *noteMedia `json:"movie,omitempty"`
	Show    *noteMedia `json:"show,omitempty"`
	Season  *noteMedia `json:"season,omitempty"`
	Episode *noteMedia `json:"episode,omitempty"`
	Person  *noteMedia `json:"person,omitempty"`
	Notes   string     `json:"notes"`
	Privacy string     `json:"privacy,omitempty"`
	Spoiler *bool      `json:"spoiler,omitempty"`
}

// updateNoteBody is the PUT /notes/{id} body.
type updateNoteBody struct {
	Notes   *string `json:"notes,omitempty"`
	Privacy string  `json:"privacy,omitempty"`
	Spoiler *bool   `json:"spoiler,omitempty"`
}

func registerNoteTools(s *Server) {
	Register(s, Spec{
		Name: "get_note", Title: "Get note", Access: Read,
		Description: "Get one of the user's notes by id: its text, privacy, spoiler flag and the item it is attached to." + vipNoteNote + untrustedNote,
	}, fetchNote)
	Register(s, Spec{
		Name: "list_user_notes", Title: "List user notes", Access: Read,
		Description: "List the user's own notes, optionally only those on movies, TV shows, seasons, episodes or people. " +
			"Each entry has the note and the item it belongs to. Takes limit and page." + vipNoteNote + untrustedNote,
	}, fetchUserNotes)
	Register(s, Spec{
		Name: "create_note", Title: "Create note", Access: Write,
		Description: "Add a note to one movie, TV show, season, episode or person, by its numeric Trakt id, with optional privacy (private, friends or public) and spoiler flag. " +
			"Returns the new note, whose id the other note tools take." + vipNoteNote + untrustedNote,
	}, createNote)
	Register(s, Spec{
		Name: "update_note", Title: "Update note", Access: Write,
		Description: "Change the text, privacy or spoiler flag of one of the user's notes. Only the arguments given change; give at least one. " +
			"Returns the updated note." + vipNoteNote + untrustedNote,
	}, updateNote)
	Register(s, Spec{
		Name: "delete_note", Title: "Delete note", Access: Destroy,
		Description: "Delete one of the user's notes. This is irreversible: Trakt removes the note for good." + vipNoteNote,
	}, deleteNote)
}

// traktNoteDetail is the note object Trakt sends, alone or inside an entry.
type traktNoteDetail struct {
	ID        int64   `json:"id"`
	Notes     *string `json:"notes"`
	Privacy   *string `json:"privacy"`
	Spoiler   bool    `json:"spoiler"`
	CreatedAt *string `json:"created_at"`
	UpdatedAt *string `json:"updated_at"`
}

func trimNoteDetail(n traktNoteDetail) NoteDetail {
	return NoteDetail{
		ID:        n.ID,
		Notes:     boundText(deref(n.Notes), maxNoteRunes),
		Privacy:   boundText(deref(n.Privacy), maxListFieldRunes),
		Spoiler:   n.Spoiler,
		CreatedAt: deref(n.CreatedAt),
		UpdatedAt: deref(n.UpdatedAt),
	}
}

// traktNote is the answer of the note endpoints. The item carries the type
// of what the note is on, and the media object when Trakt sends it.
type traktNote struct {
	traktNoteDetail
	Item *struct {
		Type string `json:"type"`
		traktMedia
		Person *traktPerson `json:"person"`
	} `json:"item"`
}

func trimNote(n traktNote) Note {
	out := Note{NoteDetail: trimNoteDetail(n.traktNoteDetail)}

	if n.Item == nil {
		return out
	}

	out.ItemType = boundText(n.Item.Type, maxListFieldRunes)
	out.MediaRef = n.Item.trim()

	if n.Item.Person != nil {
		p := trimEmbeddedPerson(*n.Item.Person)
		out.Person = &p
	}

	return out
}

func parseNote(body []byte) (Note, error) {
	raw, err := decodeAs[traktNote](body)
	if err != nil {
		return Note{}, err
	}

	return trimNote(raw), nil
}

type traktUserNote struct {
	Type       string `json:"type"`
	AttachedTo *struct {
		Type        string  `json:"type"`
		ID          *int64  `json:"id"`
		WatchedAt   *string `json:"watched_at"`
		CollectedAt *string `json:"collected_at"`
		RatedAt     *string `json:"rated_at"`
		Rating      *int    `json:"rating"`
	} `json:"attached_to"`
	Note   traktNoteDetail `json:"note"`
	Person *traktPerson    `json:"person"`
	traktMedia
}

func trimUserNote(n traktUserNote) UserNote {
	out := UserNote{
		Type:     boundText(n.Type, maxListFieldRunes),
		Note:     trimNoteDetail(n.Note),
		MediaRef: n.trim(),
	}

	if a := n.AttachedTo; a != nil {
		out.AttachedTo = &NoteAttachment{
			Type: boundText(a.Type, maxListFieldRunes), ID: deref(a.ID), WatchedAt: deref(a.WatchedAt),
			CollectedAt: deref(a.CollectedAt), RatedAt: deref(a.RatedAt), Rating: deref(a.Rating),
		}
	}

	if n.Person != nil {
		p := trimEmbeddedPerson(*n.Person)
		out.Person = &p
	}

	return out
}

// path validates the id of a note.
func (a NoteRefArgs) path() (string, error) {
	if a.NoteID <= 0 {
		return "", invalidArgument(fieldNoteID)
	}

	return strconv.Itoa(a.NoteID), nil
}

func fetchNote(ctx context.Context, c *trakt.UserClient, in NoteRefArgs) (Note, error) {
	id, err := in.path()
	if err != nil {
		return Note{}, err
	}

	return fetchParsed(func() (*http.Response, error) { return c.API().GetNotesSummary(ctx, id) }, parseNote)
}

// noteTypes are the type filters of the user notes path. UNVERIFIED: not documented by Trakt; the
// filtered spec leaves the path's type free text and lists singular values
// only for the response; this is the plural set the sibling user paths take.
var noteTypes = listItemTypes

func fetchUserNotes(ctx context.Context, c *trakt.UserClient, in UserNotesArgs) (Listing[UserNote], error) {
	typ, err := oneOf(fieldType, in.Type, mediaAll, noteTypes)
	if err != nil {
		return Listing[UserNote]{}, err
	}

	return paginateDecoded(ctx, in.PageArgs, trimUserNote, func(page, limit int) (*http.Response, error) {
		return c.API().GetUsersNotes(ctx, userMe, typ, &api.GetUsersNotesParams{Extended: extendedFullPtr(), Page: &page, Limit: &limit})
	})
}

// noteMediaOf attaches the media object to the key of its type.
func noteMediaOf(body *createNoteBody, typ string, m *noteMedia) {
	switch typ {
	case itemMovie:
		body.Movie = m
	case itemShow:
		body.Show = m
	case itemSeason:
		body.Season = m
	case itemEpisode:
		body.Episode = m
	case itemPerson:
		body.Person = m
	}
}

// noteItem validates the item of a create call into its media object.
func noteItem(a NoteItemArg) (*noteMedia, error) {
	if _, err := oneOf(fieldType, a.Type, "", noteItemTypes); err != nil || a.Type == "" {
		return nil, invalidArgument(fieldItem)
	}

	id, err := ParseID(a.ID, Numeric)
	if err != nil {
		return nil, invalidArgument(fieldItem)
	}

	ids, err := syncIDsOf(id)
	if err != nil || ids.Trakt == 0 {
		return nil, invalidArgument(fieldItem)
	}

	m := &noteMedia{}
	m.IDs.Trakt = ids.Trakt

	return m, nil
}

func createNote(ctx context.Context, c *trakt.UserClient, in CreateNoteArgs) (Note, error) {
	media, err := noteItem(in.Item)
	if err != nil {
		return Note{}, err
	}

	text, err := boundedText(fieldNotes, in.Notes, maxNoteRunes, false)
	if err != nil {
		return Note{}, err
	}

	privacy, err := oneOf(fieldPrivacy, in.Privacy, "", notePrivacies)
	if err != nil {
		return Note{}, err
	}

	body := createNoteBody{Notes: text, Privacy: privacy, Spoiler: in.Spoiler}
	noteMediaOf(&body, in.Item.Type, media)

	r, err := sendJSON(body, func(b io.Reader) (*http.Response, error) {
		return c.API().PostNotesCreateWithBody(ctx, jsonContentType, b)
	})
	if err != nil {
		return Note{}, err
	}

	return parseNote(r.body)
}

func updateNote(ctx context.Context, c *trakt.UserClient, in UpdateNoteArgs) (Note, error) {
	id, err := in.path()
	if err != nil {
		return Note{}, err
	}

	body := updateNoteBody{Spoiler: in.Spoiler}

	if in.Notes != nil {
		text, err := boundedText(fieldNotes, *in.Notes, maxNoteRunes, false)
		if err != nil {
			return Note{}, err
		}

		body.Notes = &text
	}

	if body.Privacy, err = oneOf(fieldPrivacy, in.Privacy, "", notePrivacies); err != nil {
		return Note{}, err
	}

	if body == (updateNoteBody{}) {
		return Note{}, invalidArgument(fieldChanges)
	}

	r, err := sendJSON(body, func(b io.Reader) (*http.Response, error) {
		return c.API().PutNotesUpdateWithBody(ctx, id, jsonContentType, b)
	})
	if err != nil {
		return Note{}, err
	}

	return parseNote(r.body)
}

func deleteNote(ctx context.Context, c *trakt.UserClient, in NoteRefArgs) (DeleteNoteResult, error) {
	id, err := in.path()
	if err != nil {
		return DeleteNoteResult{}, err
	}

	if _, err := readReply(func() (*http.Response, error) { return c.API().DeleteNotesDelete(ctx, id) }); err != nil {
		return DeleteNoteResult{}, err
	}

	return DeleteNoteResult{Deleted: true}, nil
}
