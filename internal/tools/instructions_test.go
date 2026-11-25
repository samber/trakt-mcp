package tools

import (
	"context"
	"regexp"
	"slices"
	"testing"
)

// toolNameInText matches the snake_case words of the server instructions
// that look like tool names: a known verb prefix, then at least one part.
var toolNameInText = regexp.MustCompile(`\b(?:add|cancel|checkin|create|delete|get|hide|list|remove|reorder|reset|search|undo|unhide|update)(?:_[a-z]+)+\b|\bcheckin\b|\bsearch\b`)

// TestInstructionsNameOnlyRegisteredTools keeps the routing rules of the
// server instructions in step with the registry: a renamed or removed tool
// must fail here, not mislead an assistant.
func TestInstructionsNameOnlyRegisteredTools(t *testing.T) {
	t.Parallel()

	var names []string
	for _, s := range NewServer(offlineDeps()).Specs() {
		names = append(names, s.Name)
	}

	found := toolNameInText.FindAllString(serverInstructions, -1)
	if len(found) < 10 {
		t.Fatalf("instructions name %d tools, want the routing rules to name at least 10", len(found))
	}

	for _, name := range found {
		if !slices.Contains(names, name) {
			t.Errorf("instructions name %q, which is not a registered tool", name)
		}
	}
}

// TestServerAdvertisesTitleAndInstructions checks that a client receives the
// display title and the instructions at initialize.
func TestServerAdvertisesTitleAndInstructions(t *testing.T) {
	t.Parallel()

	h := newHarness(t)
	res := h.connect(protocolVersion20251125).InitializeResult()

	if res.ServerInfo.Title != serverTitle {
		t.Errorf("server title = %q, want %q", res.ServerInfo.Title, serverTitle)
	}

	if res.Instructions != serverInstructions {
		t.Error("server instructions not advertised at initialize")
	}
}

// TestTitleOnlyToolsOfferNoSeasonOrEpisode checks that the tools accepting
// only a movie or a TV show do not advertise season and episode arguments
// they would refuse.
func TestTitleOnlyToolsOfferNoSeasonOrEpisode(t *testing.T) {
	t.Parallel()

	h := newHarness(t)

	res, err := h.connect(protocolVersion20251125).ListTools(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}

	titleOnly := []string{"list_aliases", "list_studios", "list_related", "hide_recommendation", "unhide_recommendation"}
	seen := 0

	for _, tool := range res.Tools {
		if !slices.Contains(titleOnly, tool.Name) {
			continue
		}

		seen++

		schema, ok := tool.InputSchema.(map[string]any)
		if !ok {
			t.Fatalf("%s: input schema is %T, want a JSON object", tool.Name, tool.InputSchema)
		}

		props, _ := schema["properties"].(map[string]any)
		for _, field := range []string{fieldSeason, fieldEpisode} {
			if _, ok := props[field]; ok {
				t.Errorf("%s advertises %s", tool.Name, field)
			}
		}
	}

	if seen != len(titleOnly) {
		t.Errorf("found %d of the %d movie-or-TV-show tools", seen, len(titleOnly))
	}
}
