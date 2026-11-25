package tools

import "testing"

// pathItem prefixes the output paths of a Typed result: the per-type
// output sits under its item key.
const pathItem = "item."

// typedOut decodes a Typed result whose item has type T.
type typedOut[T any] struct {
	Type string `json:"type"`
	Item T      `json:"item"`
}

// movieItem, showItem, seasonItem and episodeItem are the arguments naming
// one item for a tool taking an ItemRef.
func movieItem(id string) map[string]any { return map[string]any{argType: typeMovie, argID: id} }

func showItem(id string) map[string]any { return map[string]any{argType: typeShow, argID: id} }

func seasonItem(id string, season int) map[string]any {
	return map[string]any{argType: typeSeason, argID: id, fieldSeason: season}
}

func episodeItem(id string, season, episode int) map[string]any {
	return map[string]any{argType: typeEpisode, argID: id, fieldSeason: season, fieldEpisode: episode}
}

// inItem moves the output paths of want under the item of a Typed result.
func inItem(want map[string]any) map[string]any {
	out := make(map[string]any, len(want))
	for path, v := range want {
		out[pathItem+path] = v
	}

	return out
}

// inItemPaths is inItem for a list of output paths.
func inItemPaths(paths []string) []string {
	out := make([]string, 0, len(paths))
	for _, p := range paths {
		out = append(out, pathItem+p)
	}

	return out
}

func TestItemRefCheck(t *testing.T) {
	t.Parallel()

	all := []string{typeMovie, typeShow, typeSeason, typeEpisode}

	cases := []struct {
		name    string
		ref     ItemRef
		allowed []string
		// wantField is the field the error names; empty means valid.
		wantField string
	}{
		{name: "movie ok", ref: ItemRef{Type: typeMovie, ID: testMovieSlug}, allowed: all},
		{name: "show ok", ref: ItemRef{Type: typeShow, ID: testShowSlug}, allowed: all},
		{name: "season ok", ref: ItemRef{Type: typeSeason, ID: testShowSlug, Season: new(1)}, allowed: all},
		{name: "season 0, the specials", ref: ItemRef{Type: typeSeason, ID: testShowSlug, Season: new(0)}, allowed: all},
		{name: "season without season", ref: ItemRef{Type: typeSeason, ID: testShowSlug}, allowed: all, wantField: fieldSeason},
		{name: "season with episode", ref: ItemRef{Type: typeSeason, ID: testShowSlug, Season: new(1), Episode: new(1)}, allowed: all, wantField: fieldEpisode},
		{name: "episode with season only", ref: ItemRef{Type: typeEpisode, ID: testShowSlug, Season: new(1)}, allowed: all, wantField: fieldEpisode},
		{name: "episode without season", ref: ItemRef{Type: typeEpisode, ID: testShowSlug, Episode: new(1)}, allowed: all, wantField: fieldSeason},
		{name: "episode ok", ref: ItemRef{Type: typeEpisode, ID: testShowSlug, Season: new(1), Episode: new(2)}, allowed: all},
		{name: "movie with season", ref: ItemRef{Type: typeMovie, ID: testMovieSlug, Season: new(1)}, allowed: all, wantField: fieldSeason},
		{name: "movie with episode", ref: ItemRef{Type: typeMovie, ID: testMovieSlug, Episode: new(1)}, allowed: all, wantField: fieldEpisode},
		{name: "show with season", ref: ItemRef{Type: typeShow, ID: testShowSlug, Season: new(1)}, allowed: all, wantField: fieldSeason},
		{name: "type empty", ref: ItemRef{ID: testMovieSlug}, allowed: all, wantField: fieldType},
		{name: "type upper case", ref: ItemRef{Type: "Movie", ID: testMovieSlug}, allowed: all, wantField: fieldType},
		{name: "type person", ref: ItemRef{Type: itemPerson, ID: testMovieSlug}, allowed: all, wantField: fieldType},
		{name: "type show not allowed", ref: ItemRef{Type: typeShow, ID: testShowSlug}, allowed: []string{typeMovie}, wantField: fieldType},
		{name: "season below 0", ref: ItemRef{Type: typeSeason, ID: testShowSlug, Season: new(-1)}, allowed: all, wantField: fieldSeason},
		{name: "season above max", ref: ItemRef{Type: typeSeason, ID: testShowSlug, Season: new(maxSeason + 1)}, allowed: all, wantField: fieldSeason},
		{name: "episode below 1", ref: ItemRef{Type: typeEpisode, ID: testShowSlug, Season: new(1), Episode: new(0)}, allowed: all, wantField: fieldEpisode},
		{name: "episode above max", ref: ItemRef{Type: typeEpisode, ID: testShowSlug, Season: new(1), Episode: new(maxEpisode + 1)}, allowed: all, wantField: fieldEpisode},
		{name: "no id", ref: ItemRef{Type: typeMovie}, allowed: all, wantField: fieldID},
		{name: "id traversal", ref: ItemRef{Type: typeShow, ID: traversalID}, allowed: all, wantField: fieldID},
		{name: "id person imdb", ref: ItemRef{Type: typeMovie, ID: personIMDb}, allowed: all, wantField: fieldID},
		{name: "id title imdb", ref: ItemRef{Type: typeShow, ID: testShowIMDb}, allowed: all},
		{name: "numeric id", ref: ItemRef{Type: typeMovie, ID: "481"}, allowed: all},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			err := tc.ref.check(tc.allowed...)

			if tc.wantField == "" {
				if err != nil {
					t.Fatalf("check = %v, want nil", err)
				}

				return
			}

			if err == nil || err.Error() != invalid(tc.wantField) {
				t.Fatalf("check = %v, want %q", err, invalid(tc.wantField))
			}
		})
	}
}

func TestCheckType(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name    string
		value   string
		allowed []string
		ok      bool
	}{
		{name: "allowed", value: typeShow, allowed: []string{typeMovie, typeShow}, ok: true},
		{name: "not allowed", value: typeSeason, allowed: []string{typeMovie, typeShow}},
		{name: caseNameEmpty, value: "", allowed: []string{typeMovie}},
		{name: "plural", value: mediaMovies, allowed: []string{typeMovie}},
		{name: "nothing allowed", value: typeMovie},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			err := checkType(tc.value, tc.allowed...)
			if tc.ok != (err == nil) {
				t.Fatalf("checkType(%q) = %v, want ok %v", tc.value, err, tc.ok)
			}

			if err != nil && err.Error() != invalid(fieldType) {
				t.Fatalf("checkType(%q) = %q, want %q", tc.value, err, invalid(fieldType))
			}
		})
	}
}

func TestItemRefConversions(t *testing.T) {
	t.Parallel()

	ref := ItemRef{Type: typeEpisode, ID: testShowSlug, Season: new(3), Episode: new(7)}

	if got := ref.movieArgs(); got.MovieID != testShowSlug || got.Extended != nil {
		t.Errorf("movieArgs = %+v", got)
	}

	if got := ref.showRef(); got.ShowID != testShowSlug {
		t.Errorf("showRef = %+v", got)
	}

	if got := ref.seasonRef(); got.ShowID != testShowSlug || got.Season != 3 {
		t.Errorf("seasonRef = %+v", got)
	}

	if got := ref.episodeRef(); got.ShowID != testShowSlug || got.Season != 3 || got.Episode != 7 {
		t.Errorf("episodeRef = %+v", got)
	}
}
