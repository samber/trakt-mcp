package tools

import (
	"encoding/json"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"testing"
)

const (
	toolUpdateWatchlistItem = "update_watchlist_item"
	toolReorderWatchlist    = "reorder_watchlist"

	argListItemID = "list_item_id"
	argRank       = "rank"

	// testListItemID is the id of the first item of the sync_watchlist fixture.
	testListItemID = 101
	// testItemRoute is the Trakt route that updates that item.
	testItemRoute = "PUT /sync/watchlist/101"
	// testReorderRoute is the Trakt route that reorders the watchlist.
	testReorderRoute = "POST /sync/watchlist/reorder"
	// testReorderReply is Trakt's documented answer to a reorder.
	// Case names and message of the shared Trakt failure matrix.
	testCaseUnauthorized = "unauthorized"
	testCaseBadGateway   = "bad gateway"
	testCaseServerError  = "server error"
	testMsgServerError   = "Trakt returned an error (status 500)"
	testReorderReply     = `{"updated":3,"skipped_ids":[9],"list":{"item_count":3,"updated_at":"2024-03-01T20:00:00.000Z"}}`
)

func init() {
	accountAccess[toolUpdateWatchlistItem] = Write
	accountAccess[toolReorderWatchlist] = Write

	accountCaseSets = append(accountCaseSets, watchlistEditCases)
}

var watchlistEditCases = []accountCase{
	{
		tool: toolUpdateWatchlistItem, args: map[string]any{argListItemID: testListItemID, argNotes: "Rewatch"},
		route: testItemRoute, status: http.StatusNoContent, object: true, want: map[string]any{"updated": true},
	},
	{
		tool: toolReorderWatchlist, args: map[string]any{argRank: []int{3, 1, 2}},
		route: testReorderRoute, status: http.StatusOK, fixture: "sync_watchlist_reorder", object: true,
		want: map[string]any{"updated": 3, "skipped_ids.0": 9, "item_count": 3, "updated_at": testWatchedAt},
	},
}

func TestWatchlistEditBodies(t *testing.T) {
	t.Parallel()

	t.Run("update sends notes only", func(t *testing.T) {
		t.Parallel()

		got := recorded(t, toolUpdateWatchlistItem, map[string]any{argListItemID: testListItemID, argNotes: "Rewatch"}, testItemRoute, http.StatusNoContent, "")
		if len(got) != 1 || got[argNotes] != "Rewatch" {
			t.Errorf("body = %v, want only the notes", got)
		}
	})

	t.Run("empty notes clear the note with null", func(t *testing.T) {
		t.Parallel()

		got := recorded(t, toolUpdateWatchlistItem, map[string]any{argListItemID: testListItemID, argNotes: ""}, testItemRoute, http.StatusNoContent, "")
		if v, ok := got[argNotes]; !ok || v != nil {
			t.Errorf("body = %v, want notes null sent", got)
		}
	})

	t.Run("reorder sends the ids in order", func(t *testing.T) {
		t.Parallel()

		got := recorded(t, toolReorderWatchlist, map[string]any{argRank: []int{3, 1, 2}}, testReorderRoute, http.StatusOK, testReorderReply)

		ids, ok := got[argRank].([]any)
		if !ok || len(got) != 1 || len(ids) != 3 || ids[0] != 3.0 || ids[1] != 1.0 || ids[2] != 2.0 {
			t.Errorf("body = %v, want rank [3 1 2]", got)
		}
	})
}

func TestWatchlistEditArguments(t *testing.T) {
	t.Parallel()

	ranks := func(n int) []int {
		ids := make([]int, n)
		for i := range ids {
			ids[i] = i + 1
		}

		return ids
	}
	update := func(id int, notes string) map[string]any {
		return map[string]any{argListItemID: id, argNotes: notes}
	}
	reorder := func(ids []int) map[string]any { return map[string]any{argRank: ids} }

	cases := []argCase{
		{name: "update id zero", tool: toolUpdateWatchlistItem, args: update(0, "x"), wantErr: invalid(fieldListItemID)},
		{name: "update id negative", tool: toolUpdateWatchlistItem, args: update(-1, "x"), wantErr: invalid(fieldListItemID)},
		{name: "update notes over limit", tool: toolUpdateWatchlistItem, args: update(testListItemID, strings.Repeat("a", maxNotesRunes+1)), wantErr: invalid(fieldNotes)},
		{
			name: "update notes at limit", tool: toolUpdateWatchlistItem, args: update(testListItemID, strings.Repeat("é", maxNotesRunes)),
			route: testItemRoute, body: "",
		},
		{name: "reorder empty", tool: toolReorderWatchlist, args: reorder([]int{}), wantErr: invalid(fieldRank)},
		{name: "reorder duplicate", tool: toolReorderWatchlist, args: reorder([]int{3, 1, 3}), wantErr: invalid(fieldRank)},
		{name: "reorder zero id", tool: toolReorderWatchlist, args: reorder([]int{1, 0}), wantErr: invalid(fieldRank)},
		{name: "reorder negative id", tool: toolReorderWatchlist, args: reorder([]int{1, -2}), wantErr: invalid(fieldRank)},
		{name: "reorder over limit", tool: toolReorderWatchlist, args: reorder(ranks(maxReorderIDs + 1)), wantErr: invalid(fieldRank)},
		{name: "reorder at limit", tool: toolReorderWatchlist, args: reorder(ranks(maxReorderIDs)), route: testReorderRoute, body: testReorderReply},
		{name: "reorder one id", tool: toolReorderWatchlist, args: reorder([]int{7}), route: testReorderRoute, body: testReorderReply},
	}

	runArgCases(t, cases)
}

func TestWatchlistEditPathCarriesTheID(t *testing.T) {
	t.Parallel()

	const id = 4242

	h := newHarness(t)
	h.trakt.respond("PUT /sync/watchlist/"+strconv.Itoa(id), http.StatusNoContent, "", nil)
	h.ok(toolUpdateWatchlistItem, map[string]any{argListItemID: id, argNotes: "x"}, nil)
}

func TestWatchlistEditTraktFailures(t *testing.T) {
	t.Parallel()

	runTraktFailureMatrix(t, []failureTool{
		{toolUpdateWatchlistItem, map[string]any{argListItemID: testListItemID, argNotes: "x"}, testItemRoute, false},
		{toolReorderWatchlist, map[string]any{argRank: []int{2, 1}}, testReorderRoute, true},
	})
}

func TestWatchlistEditDescriptionsMentionVIP(t *testing.T) {
	t.Parallel()

	tools := listTools(t, NewServer(offlineDeps()))

	for _, name := range []string{toolUpdateWatchlistItem} {
		if d := tools[name].Description; !strings.Contains(d, "VIP") {
			t.Errorf("%s description %q does not mention Trakt VIP", name, d)
		}
	}
}

// TestUpdateWatchlistItemNotesIsRequired requires notes in the input schema's
// required list: were it optional, a call without notes would send an empty
// note and silently clear the stored one.
func TestUpdateWatchlistItemNotesIsRequired(t *testing.T) {
	t.Parallel()

	tool := listTools(t, NewServer(offlineDeps()))[toolUpdateWatchlistItem]

	raw, err := json.Marshal(tool.InputSchema)
	if err != nil {
		t.Fatal(err)
	}

	var schema struct {
		Required []string `json:"required"`
	}

	if err := json.Unmarshal(raw, &schema); err != nil {
		t.Fatal(err)
	}

	if !slices.Contains(schema.Required, argNotes) {
		t.Errorf("required = %v, want %q listed", schema.Required, argNotes)
	}
}
