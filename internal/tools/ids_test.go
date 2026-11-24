package tools

import (
	"strings"
	"testing"
)

func TestParseID(t *testing.T) {
	t.Parallel()

	const all = Slug | Numeric | IMDbTitle | IMDbPerson

	cases := []struct {
		name  string
		in    string
		allow IDKinds
		ok    bool
	}{
		{"slug", "breaking-bad", all, true},
		{"numeric", "1388", all, true},
		{"imdb title", "tt0903747", all, true},
		{"imdb person", "nm0186505", all, true},
		{"empty", "", all, false},
		{"parent path", "../x", all, false},
		{"slash", "a/b", all, false},
		{"query", "a?b=1", all, false},
		{"fragment", "a#b", all, false},
		{"percent escape", "a%2Fb", all, false},
		{"space", "a b", all, false},
		{"unicode", "amélie", all, false},
		{"too long", strings.Repeat("a", 200), all, false},
		{"longest accepted", strings.Repeat("a", maxIDLength), all, true},
		{"uppercase", "Breaking-Bad", all, false},
		{"uppercase imdb", "TT0903747", all, false},
		{"imdb prefix only", "tt", Slug | IMDbTitle, true},
		{"numeric not allowed", "1388", IMDbTitle, false},
		{"numeric as slug", "1388", Slug, true},
		{"person id for a title", "nm0186505", Slug | Numeric | IMDbTitle, false},
		{"title id for a person", "tt0903747", Slug | Numeric | IMDbPerson, false},
		{"slug not allowed", "breaking-bad", Numeric | IMDbTitle, false},
		{"newline", "a\nb", all, false},
		{"trailing newline", "slug\n", all, false},
		{"trailing space", "slug ", all, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()

			id, err := ParseID(c.in, c.allow)
			if c.ok && (err != nil || string(id) != c.in) {
				t.Fatalf("ParseID(%q) = %q, %v; want it accepted unchanged", c.in, id, err)
			}

			if !c.ok && err == nil {
				t.Fatalf("ParseID(%q) = %q, want an error", c.in, id)
			}
		})
	}
}

func TestIDArgHidesInput(t *testing.T) {
	t.Parallel()

	_, err := idArg("movie_id", "../secret?x=1", Slug)
	if err == nil {
		t.Fatal("want an error")
	}

	if got := err.Error(); got != "invalid argument: movie_id" {
		t.Errorf("error = %q, want the field only", got)
	}
}
