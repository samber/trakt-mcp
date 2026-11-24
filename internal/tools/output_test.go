package tools

import (
	"encoding/json"
	"strings"
	"testing"
	"unicode/utf8"
)

func TestNonNilMarshalsEmptyArray(t *testing.T) {
	t.Parallel()

	var nilStrings []string

	raw, err := json.Marshal(NonNil(nilStrings))
	if err != nil {
		t.Fatal(err)
	}

	if string(raw) != "[]" {
		t.Errorf("NonNil(nil) = %s, want []", raw)
	}

	full := []string{"a"}
	if got := NonNil(full); len(got) != 1 || &got[0] != &full[0] {
		t.Error("NonNil copied a non-nil slice")
	}

	raw, err = json.Marshal(Listing[string]{})
	if err != nil {
		t.Fatal(err)
	}

	if string(raw) != `{"items":[]}` {
		t.Errorf("empty Listing = %s, want {\"items\":[]}", raw)
	}
}

func TestBoundText(t *testing.T) {
	t.Parallel()

	if got := boundText("short", maxOverviewRunes); got != "short" {
		t.Errorf("short text changed: %q", got)
	}

	long := strings.Repeat("é", maxOverviewRunes+50)

	got := boundText(long, maxOverviewRunes)
	if n := utf8.RuneCountInString(got); n > maxOverviewRunes {
		t.Errorf("bounded text has %d runes, want at most %d", n, maxOverviewRunes)
	}

	if !utf8.ValidString(got) || !strings.HasSuffix(got, truncationMark) {
		t.Errorf("bounded text is not valid UTF-8 ending with the mark: %q", got[len(got)-10:])
	}

	if got := boundText(string([]byte{0xff, 'a'}), maxOverviewRunes); !utf8.ValidString(got) {
		t.Errorf("invalid UTF-8 kept: %q", got)
	}
}
