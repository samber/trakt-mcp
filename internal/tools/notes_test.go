package tools

import (
	"net/http"
	"strings"
	"testing"
)

const (
	toolFetchNote      = "get_note"
	toolFetchUserNotes = "list_user_notes"
	toolCreateNote     = "create_note"
	toolUpdateNote     = "update_note"
	toolDeleteNote     = "delete_note"

	argNoteID  = "note_id"
	argSpoiler = "spoiler"
	argItem    = "item"

	// testNoteID is the id of the note in the fixtures.
	testNoteID = 4242
	// testNoteText is the note text of the fixtures.
	testNoteText = "Great opening scene"

	routeNotes      = "/notes"
	routeNote       = routeNotes + "/4242"
	routeUserNotes  = "GET /users/me/notes/"
	fixtureNote     = "note"
	fixtureUserNote = "user_notes"

	// testNoteMovieTitle is the title of the movie in the note fixtures.
	testNoteMovieTitle = "Heat"
	// testPrivacyPrivate and testPrivacyFriends are note visibilities.
	testPrivacyPrivate = "private"
	testPrivacyFriends = "friends"
	// testCaseNotFound names the not found failure case.
	testCaseNotFound = "not found"
)

func init() {
	catalogCaseSets = append(catalogCaseSets, noteCatalogCases)
	accountCaseSets = append(accountCaseSets, noteWriteCases)
	accountAccess[toolCreateNote] = Write
	accountAccess[toolUpdateNote] = Write
	accountAccess[toolDeleteNote] = Destroy
}

var noteWant = map[string]any{
	"id": testNoteID, "notes": testNoteText, "privacy": testPrivacyFriends, "spoiler": true, "item_type": itemMovie,
	"created_at": "2024-02-01T10:00:00.000Z", "updated_at": "2024-02-02T11:00:00.000Z", "movie.title": testNoteMovieTitle,
}

var noteCatalogCases = []catalogCase{
	{tool: toolFetchNote, args: map[string]any{argNoteID: testNoteID}, route: "GET " + routeNote, fixture: fixtureNote, object: true, want: noteWant},
	{
		tool: toolFetchUserNotes, args: merge(map[string]any{argType: mediaMovies}, pagedArgs), route: routeUserNotes + mediaMovies,
		fixture: fixtureUserNote, headers: pagedHeaders, wantQuery: map[string]string{fieldPage: "1", fieldLimit: "2"},
		want: map[string]any{
			pathFirstItemType: itemMovie, "items.0.note.id": testNoteID, "items.0.note.notes": testNoteText, "items.0.note.privacy": testPrivacyPrivate,
			"items.0.attached_to.type": "history", "items.0.attached_to.id": 777, pathFirstMovieTitle: testNoteMovieTitle,
			"items.1.type": itemPerson, "items.1.attached_to.rating": 9, "items.1.person.name": "Michael Mann", pathPageCount: 3,
		},
		absent: []string{"items.0.person", "items.1.note.created_at"},
	},
	{tool: toolFetchUserNotes, args: map[string]any{}, route: routeUserNotes + mediaAll, fixture: fixtureUserNote, want: map[string]any{"items.0.note.id": testNoteID}},
}

var noteWriteCases = []accountCase{
	{
		tool: toolCreateNote, args: map[string]any{argItem: itemArg(itemMovie, testMovieTraktID), argNotes: testNoteText},
		route: "POST " + routeNotes, status: http.StatusCreated, fixture: fixtureNote, object: true, want: noteWant,
	},
	{
		tool: toolUpdateNote, args: map[string]any{argNoteID: testNoteID, argNotes: testNoteText}, route: "PUT " + routeNote,
		fixture: fixtureNote, object: true, want: noteWant,
	},
	{
		tool: toolDeleteNote, args: map[string]any{argNoteID: testNoteID}, route: "DELETE " + routeNote,
		status: http.StatusNoContent, object: true, want: map[string]any{keyDeleted: true},
	},
}

func TestCreateNoteBody(t *testing.T) {
	t.Parallel()

	for _, typ := range noteItemTypes {
		t.Run(typ, func(t *testing.T) {
			t.Parallel()

			args := map[string]any{argItem: itemArg(typ, testMovieTraktID), argNotes: " " + testNoteText + " ", argPrivacy: testPrivacyPublic, argSpoiler: false}
			got := recorded(t, toolCreateNote, args, "POST "+routeNotes, http.StatusCreated, fixture(t, fixtureNote))

			media, ok := got[typ].(map[string]any)
			if !ok || len(got) != 4 || got[argNotes] != testNoteText || got[argPrivacy] != testPrivacyPublic || got[argSpoiler] != false {
				t.Fatalf("body = %v, want %s, notes, privacy, spoiler only", got, typ)
			}

			ids, _ := media["ids"].(map[string]any)
			if len(media) != 1 || ids["trakt"] != float64(481) {
				t.Errorf("media = %v, want ids.trakt 481", media)
			}
		})
	}

	t.Run("notes only", func(t *testing.T) {
		t.Parallel()

		got := recorded(t, toolCreateNote, map[string]any{argItem: itemArg(itemShow, testMovieTraktID), argNotes: "x"}, "POST "+routeNotes, http.StatusCreated, fixture(t, fixtureNote))
		if len(got) != 2 {
			t.Errorf("body = %v, want show and notes only", got)
		}
	})
}

func TestUpdateNoteBody(t *testing.T) {
	t.Parallel()

	got := recorded(t, toolUpdateNote, map[string]any{argNoteID: testNoteID, argSpoiler: false, argPrivacy: testPrivacyPrivate}, "PUT "+routeNote, http.StatusOK, fixture(t, fixtureNote))
	if len(got) != 2 || got[argSpoiler] != false || got[argPrivacy] != testPrivacyPrivate {
		t.Errorf("body = %v, want spoiler and privacy only", got)
	}
}

func TestNoteArguments(t *testing.T) {
	t.Parallel()

	long := func(n int) string { return strings.Repeat("é", n) }
	item := itemArg(itemMovie, testMovieTraktID)
	create := func(extra map[string]any) map[string]any {
		return merge(map[string]any{argItem: item, argNotes: "x"}, extra)
	}
	update := func(extra map[string]any) map[string]any { return merge(map[string]any{argNoteID: testNoteID}, extra) }
	withItem := func(typ, id string) map[string]any { return create(map[string]any{argItem: itemArg(typ, id)}) }

	cases := []argCase{
		{name: "fetch note 0", tool: toolFetchNote, args: map[string]any{argNoteID: 0}, wantErr: invalid(fieldNoteID)},
		{name: "fetch note -1", tool: toolFetchNote, args: map[string]any{argNoteID: -1}, wantErr: invalid(fieldNoteID)},
		{name: "delete note 0", tool: toolDeleteNote, args: map[string]any{argNoteID: 0}, wantErr: invalid(fieldNoteID)},
		{name: "delete note -1", tool: toolDeleteNote, args: map[string]any{argNoteID: -1}, wantErr: invalid(fieldNoteID)},
		{name: "update note 0", tool: toolUpdateNote, args: map[string]any{argNoteID: 0, argNotes: "x"}, wantErr: invalid(fieldNoteID)},
		{name: "update note -1", tool: toolUpdateNote, args: map[string]any{argNoteID: -1, argNotes: "x"}, wantErr: invalid(fieldNoteID)},
		{name: "update nothing", tool: toolUpdateNote, args: update(nil), wantErr: invalid(fieldChanges)},
		{name: "update empty notes", tool: toolUpdateNote, args: update(map[string]any{argNotes: ""}), wantErr: invalid(fieldNotes)},
		{name: "update 501 runes", tool: toolUpdateNote, args: update(map[string]any{argNotes: long(maxNoteRunes + 1)}), wantErr: invalid(fieldNotes)},
		{name: "update bad privacy", tool: toolUpdateNote, args: update(map[string]any{argPrivacy: "x"}), wantErr: invalid(fieldPrivacy)},
		{name: "create empty notes", tool: toolCreateNote, args: create(map[string]any{argNotes: ""}), wantErr: invalid(fieldNotes)},
		{name: "create blank notes", tool: toolCreateNote, args: create(map[string]any{argNotes: testBlankName}), wantErr: invalid(fieldNotes)},
		{name: "create 501 runes", tool: toolCreateNote, args: create(map[string]any{argNotes: long(maxNoteRunes + 1)}), wantErr: invalid(fieldNotes)},
		{name: "create bad privacy", tool: toolCreateNote, args: create(map[string]any{argPrivacy: "x"}), wantErr: invalid(fieldPrivacy)},
		{name: "create upper privacy", tool: toolCreateNote, args: create(map[string]any{argPrivacy: "Public"}), wantErr: invalid(fieldPrivacy)},
		{name: "create slug id", tool: toolCreateNote, args: withItem(itemMovie, testMovieSlug), wantErr: invalid(fieldItem)},
		{name: "create imdb id", tool: toolCreateNote, args: withItem(itemMovie, testEpisodeIMDb), wantErr: invalid(fieldItem)},
		{name: "create zero id", tool: toolCreateNote, args: withItem(itemMovie, "0"), wantErr: invalid(fieldItem)},
		{name: "create bad type", tool: toolCreateNote, args: withItem("list", "1"), wantErr: invalid(fieldItem)},
		{name: "create empty type", tool: toolCreateNote, args: withItem("", "1"), wantErr: invalid(fieldItem)},
		{name: "fetch bad type", tool: toolFetchUserNotes, args: map[string]any{argType: testSortTraversal}, wantErr: invalid(fieldType)},
		// Boundary values: accepted.
		{name: "create 500 runes", tool: toolCreateNote, args: create(map[string]any{argNotes: long(maxNoteRunes)}), route: "POST " + routeNotes, body: fixture(t, fixtureNote)},
	}

	for _, p := range notePrivacies {
		cases = append(cases, argCase{name: "create privacy " + p, tool: toolCreateNote, args: create(map[string]any{argPrivacy: p}), route: "POST " + routeNotes, body: fixture(t, fixtureNote)})
	}

	runArgCases(t, cases)
}

func TestNoteTraktFailures(t *testing.T) {
	t.Parallel()

	tools := []struct {
		tool  string
		args  map[string]any
		route string
		body  bool
	}{
		{toolFetchNote, map[string]any{argNoteID: testNoteID}, "GET " + routeNote, true},
		{toolFetchUserNotes, map[string]any{}, routeUserNotes + mediaAll, true},
		{toolCreateNote, map[string]any{argItem: itemArg(itemMovie, testMovieTraktID), argNotes: "x"}, "POST " + routeNotes, true},
		{toolUpdateNote, map[string]any{argNoteID: testNoteID, argNotes: "x"}, "PUT " + routeNote, true},
		{toolDeleteNote, map[string]any{argNoteID: testNoteID}, "DELETE " + routeNote, false},
	}

	hostile := `{"error_description":"` + injectionMarker + `"}`
	failures := []struct {
		name   string
		status int
		want   string
	}{
		{testCaseNotFound, http.StatusNotFound, msgNotFound},
		{testCaseUnauthorized, http.StatusUnauthorized, msgUpstreamAuth},
		{testCaseAccountLimit, statusTraktAccountLimit, msgAccountLimit},
		{testCaseVIPRequired, http.StatusUpgradeRequired, msgVIPRequired},
		{testCaseServerError, http.StatusInternalServerError, testMsgServerError},
		{testCaseBadGateway, http.StatusBadGateway, testMsgBadGateway},
	}

	for _, tl := range tools {
		for _, f := range failures {
			t.Run(tl.tool+"/"+f.name, func(t *testing.T) {
				t.Parallel()

				h := newHarness(t)
				h.trakt.respond(tl.route, f.status, hostile, nil)
				h.fail(tl.tool, tl.args, f.want)

				if strings.Contains(h.logs.String(), injectionMarker) {
					t.Error("Trakt's message reached the logs")
				}
			})
		}

		t.Run(tl.tool+"/rate limited", func(t *testing.T) {
			t.Parallel()

			h := newHarness(t)
			h.trakt.respond(tl.route, testStatusRateLimited, hostile, nil)

			res := h.call(tl.tool, tl.args)
			if !res.IsError || !strings.Contains(resultText(res), "rate limited") || strings.Contains(resultText(res), injectionMarker) {
				t.Errorf("result = %v %q, want a rate limit error without Trakt text", res.IsError, resultText(res))
			}
		})

		if !tl.body {
			continue
		}

		t.Run(tl.tool+"/malformed body", func(t *testing.T) {
			t.Parallel()

			h := newHarness(t)
			h.trakt.respond(tl.route, http.StatusOK, `{"not": "closed"`, nil)
			h.fail(tl.tool, tl.args, msgInternal)
		})
	}
}

func TestNoteDescriptions(t *testing.T) {
	t.Parallel()

	tools := listTools(t, NewServer(offlineDeps()))

	if d := tools[toolDeleteNote].Description; !strings.Contains(d, "irreversible") {
		t.Errorf("delete_note description %q does not say it is irreversible", d)
	}

	for _, name := range []string{toolFetchNote, toolFetchUserNotes, toolCreateNote, toolUpdateNote, toolDeleteNote} {
		if d := tools[name].Description; !strings.Contains(d, "Free Trakt accounts can keep only a few notes") {
			t.Errorf("%s description %q does not state the free-account note cap", name, d)
		}
	}
}

func TestNoteOutputBounded(t *testing.T) {
	t.Parallel()

	long := strings.Repeat("é", 5000)
	h := newHarness(t)
	h.trakt.respond("GET "+routeNote, http.StatusOK, `{"id":1,"notes":"`+long+`","privacy":"`+long+`","item":{"type":"`+long+`"}}`, nil)

	var out Note

	h.ok(toolFetchNote, map[string]any{argNoteID: testNoteID}, &out)

	if len([]rune(out.Notes)) > maxNoteRunes || len([]rune(out.Privacy)) > maxListFieldRunes || len([]rune(out.ItemType)) > maxListFieldRunes {
		t.Errorf("note = %+v, want bounded text", out)
	}
}

// TestUserNotesSingularType checks the singular media types of the notes
// filter.
func TestUserNotesSingularType(t *testing.T) {
	t.Parallel()

	path := func(plural string) string { return "/users/me/notes/" + plural }

	runArgCases(t, singularFilterCases(singularFilter{
		tool:      toolFetchUserNotes,
		args:      func(typ string) map[string]any { return map[string]any{argType: typ} },
		path:      path,
		accepted:  noteTypes,
		emptyPath: path(mediaAll),
	}))
}
