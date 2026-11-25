# Tool consolidation and naming

Status: draft, not approved. No code changes until approved.

## Problem

The server exposes 143 tools. About 60 of them are the same operation repeated per media type
(`fetch_movie_comments`, `fetch_show_comments`, `fetch_season_comments`, `fetch_episode_comments`).
Every tool name starts with `fetch_`, so names do not help a model tell tools apart. Write tools
already take mixed item types (`add_ratings`, `add_to_history`), so reads and writes follow
different shapes.

## Goals

- Fewer tools, each with one clear job and a description that says when to pick it.
- One naming scheme: `verb_object` in snake_case, one verb per kind of operation.
- Same argument names across tools (`type`, `id`, `season`, `episode`, `limit`, `page`).
- Keep every Trakt capability that exists today.

## Non-goals

- New Trakt endpoints.
- A single `do_anything(action, payload)` tool. Schemas stay strict enough to validate.
- Changing output content, trimming, paging or error handling.

## Anti-goals

- A merged tool whose schema is so loose that a model cannot tell which arguments apply.
- Breaking the directory requirements: every tool keeps `title`, `readOnlyHint`, `destructiveHint`
  and `openWorldHint` (`TestToolAnnotations`).

## Conventions this follows

- Anthropic, [Writing effective tools for agents](https://www.engineering.fyi/article/writing-effective-tools-for-agents-with-agents):
  consolidate overlapping tools, namespace related tools, measure with evals.
- [AWS MCP tool organization](https://docs.aws.amazon.com/prescriptive-guidance/latest/mcp-strategies/mcp-tool-strategy-organization.html):
  consider splitting a server above 50 tools.
- [MCP Toolbox style guide](https://mcp-toolbox.dev/reference/style-guide/): `action_resource`
  names, a ceiling near 40 tools per server.
- Fully qualified name `mcp__<server>__<tool>` must stay under 64 characters.

## Proposed naming scheme

| Verb                                                                | Use                                  |
| ------------------------------------------------------------------- | ------------------------------------ |
| `get_`                                                              | one entity or one fixed-shape answer |
| `list_`                                                             | a collection, paged                  |
| `search_`                                                           | text search                          |
| `add_` / `remove_` / `update_` / `create_` / `delete_` / `reorder_` | writes, unchanged                    |
| `hide_` / `unhide_` / `reset_` / `undo_reset_`                      | state toggles, unchanged             |

`fetch_` disappears.

## Shared arguments for merged tools

- `type`: `movie`, `show`, `season` or `episode`. Each tool lists the values it accepts.
- `id`: Trakt slug, numeric Trakt id or IMDb id of the movie or show.
- `season`: required when `type` is `season` or `episode`.
- `episode`: required when `type` is `episode`.

The handler validates the combination and returns `invalid argument: season is required for
type episode`. JSON Schema cannot express this cleanly across all clients, so the tool
description states it too.

## Mapping

### Merged by `type` (movie, show, season, episode)

| New tool            | Replaces                                                                                  |
| ------------------- | ----------------------------------------------------------------------------------------- |
| `get_summary`       | `fetch_movie_summary`, `fetch_show_summary`, `fetch_season_info`, `fetch_episode_summary` |
| `list_comments`     | `fetch_{movie,show,season,episode}_comments`                                              |
| `get_ratings`       | `fetch_{movie,show,season,episode}_ratings` (rating distribution)                         |
| `list_people`       | `fetch_{movie,show,season,episode}_people`                                                |
| `list_videos`       | `fetch_{movie,show,season,episode}_videos`                                                |
| `list_translations` | `fetch_{movie,show,season,episode}_translations`                                          |
| `get_stats`         | `fetch_{movie,show,season,episode}_stats`                                                 |
| `list_item_lists`   | `fetch_{movie,show,season,episode}_lists` (public lists containing the item)              |
| `list_watching`     | `fetch_{movie,show,season,episode}_watching`                                              |
| `get_watchnow`      | `fetch_{movie,show,episode}_watchnow`                                                     |

### Merged by `type` (movie, show)

| New tool                | Replaces                                                                                    |
| ----------------------- | ------------------------------------------------------------------------------------------- |
| `list_aliases`          | `fetch_movie_aliases`, `fetch_show_aliases`                                                 |
| `list_studios`          | `fetch_movie_studios`, `fetch_show_studios`                                                 |
| `list_recommendations`  | `fetch_movie_recommendations`, `fetch_show_recommendations`                                 |
| `hide_recommendation`   | `hide_movie_recommendation`, `hide_show_recommendation`                                     |
| `unhide_recommendation` | `unhide_movie_recommendation`, `unhide_show_recommendation`                                 |
| `list_related`          | `fetch_related_movies`, `fetch_related_shows`                                               |
| `list_trending`         | `fetch_trending_movies`, `fetch_trending_shows`                                             |
| `list_popular`          | `fetch_popular_movies`, `fetch_popular_shows`                                               |
| `list_anticipated`      | `fetch_anticipated_movies`, `fetch_anticipated_shows`                                       |
| `list_favorited`        | `fetch_favorited_movies`, `fetch_favorited_shows`                                           |
| `list_played`           | `fetch_played_movies`, `fetch_played_shows`                                                 |
| `list_watched`          | `fetch_watched_movies`, `fetch_watched_shows`                                               |
| `list_person_credits`   | `fetch_person_movies`, `fetch_person_shows`                                                 |
| `checkin`               | `checkin_to_movie`, `checkin_to_show`                                                       |
| `search`                | `search_movies`, `search_shows`, `search_movies_exact`, `search_shows_exact` (`exact` flag) |

### Merged by `kind`

| New tool        | Replaces                                                                                                         |
| --------------- | ---------------------------------------------------------------------------------------------------------------- |
| `list_calendar` | `fetch_calendar_{shows,new_shows,show_premieres,show_finales,movies,dvd,streaming}`; `kind` selects the calendar |
| `list_lists`    | `fetch_trending_lists`, `fetch_popular_lists`; `kind` is `trending` or `popular`                                 |

### Renamed only (`fetch_` to `get_` or `list_`)

| Old                                                                                            | New                                                                                       |
| ---------------------------------------------------------------------------------------------- | ----------------------------------------------------------------------------------------- |
| `fetch_boxoffice_movies`                                                                       | `list_boxoffice`                                                                          |
| `fetch_collection`, `fetch_watchlist`, `fetch_history`, `fetch_favorites`                      | `list_collection`, `list_watchlist`, `list_history`, `list_favorites`                     |
| `fetch_ratings`                                                                                | `list_user_ratings` (avoids clash with `get_ratings`)                                     |
| `fetch_user_watched`, `fetch_playback_progress`, `fetch_up_next`, `fetch_hidden_progress`      | `list_user_watched`, `list_playback_progress`, `list_up_next`, `list_hidden_progress`     |
| `fetch_show_seasons`, `fetch_season_episodes`                                                  | `list_seasons`, `list_season_episodes`                                                    |
| `fetch_show_next_episode`, `fetch_show_last_episode`                                           | `get_next_episode`, `get_last_episode`                                                    |
| `fetch_show_progress`, `fetch_show_collection_progress`                                        | `get_show_progress`, `get_show_collection_progress`                                       |
| `fetch_show_certifications`, `fetch_movie_releases`                                            | `list_show_certifications`, `list_movie_releases`                                         |
| `fetch_person_summary`                                                                         | `get_person`                                                                              |
| `fetch_comment`, `fetch_comment_replies`                                                       | `get_comment`, `list_comment_replies`                                                     |
| `fetch_list`, `fetch_list_items`, `fetch_user_lists`                                           | `get_list`, `list_list_items`, `list_user_lists`                                          |
| `fetch_note`, `fetch_user_notes`                                                               | `get_note`, `list_user_notes`                                                             |
| `fetch_smart_lists`, `fetch_smart_list_items`                                                  | `list_smart_lists`, `list_smart_list_items`                                               |
| `fetch_watchnow_sources`                                                                       | `list_watchnow_sources`                                                                   |
| `fetch_genres`, `fetch_countries`, `fetch_languages`, `fetch_certifications`, `fetch_networks` | `list_genres`, `list_countries`, `list_languages`, `list_certifications`, `list_networks` |

### Unchanged

All write tools: `add_*`, `remove_*`, `update_*`, `create_*`, `delete_*`, `reorder_*`,
`hide_from_progress`, `unhide_from_progress`, `reset_show_progress`, `undo_reset_show_progress`,
`cancel_checkin`, `remove_playback_item`, `search_by_id`.

## Resulting count

143 tools become about 90 (53 removed by merging). That is below the 100-tool range measured
in the [DEV Community study](https://dev.to/yahhi/how-many-tools-should-an-mcp-server-have-i-measured-5-to-100-in-claude-code-codex-and-gemini-3hap)
but still above the 40-50 ceilings in the AWS and MCP Toolbox guidance. An earlier estimate of
about 65 was too low.

Optional further cuts, each trading clarity for count:

- Merge the five reference lists (`genres`, `countries`, `languages`, `certifications`, `networks`)
  into one `list_reference(kind)`: about 86 tools.
- Offer toolsets (read-only core, writes, calendars, lists and notes) selected at connection time:
  one server, fewer tools per session. Needs MCP client support and an OAuth or URL parameter.

## Approaches considered

1. **Merge by `type`, rename to `get_`/`list_` (proposed).** One breaking change. Most reduction,
   one schema per concept.
2. **Merge only, keep `fetch_`.** Same reduction, names stay noisy.
3. **Rename only.** No reduction, so the main problem stays.

## Open design questions

1. **Output shape of merged tools.** Movie, Show, Season and Episode results differ. Options:
   a union (`oneOf`) in the output schema, or an envelope `{type, item}` with an `any` item.
   Needs a spike with the go-sdk typed output schema before choosing. Merged tools whose
   results share a shape (`list_comments`, `list_videos`, `list_translations`) are unaffected.
2. **Per-type arguments.** `list_translations` takes `language`; `list_comments` takes `sort`;
   `get_watchnow` takes `country`. Check each merged pair accepts the same arguments before merging.
3. **Compatibility.** Tool names change for clients already connected to the hosted instance.
   Options: hard break (clients refresh the tool list), or keep old names as aliases for one
   release. Aliases keep the count high, so the proposal is a hard break.
4. **Evals.** Measure tool selection before and after on a fixed prompt set, as Anthropic advises.
   Decide whether this ships with the change or after.

## Files touched

- `internal/tools/*.go`: registrations, argument types, handlers, one test file per merged area.
- `TestRegistryHas143Tools`: rename with the new count.
- `README.md`, `plugin/openai/` (bump `version` in `plugin.json`, run `make plugin-openai`),
  `site/` tool lists, `docs/directory-submission.md`.
- `docs/superpowers/specs/2026-11-23-trakt-mcp-design.md`: tool table.

## Rollout

1. Spike the output-shape question on `get_summary` (movie and show only).
2. Merge area by area, one commit each, gates green after each (`gofmt`, `go vet`, `make lint`,
   `make test`).
3. Rename pass.
4. Update README, plugin, site, specs; run `make plugin-openai`.

## Decisions

### Output shape of merged tools (open question 1): envelope

Merged tools whose result shape depends on `type` return the envelope `Typed`
(`internal/tools/summary.go`):

```go
type Typed struct {
	Type string `json:"type"` // repeats the type argument
	Item any    `json:"item"` // the legacy handler's output, unchanged
}
```

- The handler returns `(Typed, error)`; `typed(in.Type, item, err)` wraps a legacy handler's
  result. `Typed.resultCount()` delegates to the item, so logs and metrics match the legacy tool.
- The output schema is inferred as for every tool: `Register` calls `outputSchema[Typed]()`
  (`jsonschema.For`). No hand-built schema. It reads
  `{"type":"object","required":["type","item"],"properties":{"type":{"type":"string"},"item":{}}}`:
  `item` is the empty schema, which accepts any value.
- Evidence (spike on `get_summary`, all four types): go-sdk accepted the schema at `AddTool`,
  `TestToolAnnotations` passed, output validation passed on real calls, and
  `requireSerializedOutput` passed (text block equals structured content), e.g.
  `{"type":"movie","item":{"title":"The Matrix",...}}`. The `oneOf` fallback was not needed.
- Cost: `item` carries no field schema, so the tool description must name the fields per type.
- Tests read output paths under `item.` (`inItem`, `inItemPaths` in `itemref_test.go`).
- Merged tools whose types share one output type (`list_comments`, `list_videos`,
  `list_translations`, ratings, stats) return that type directly, without the envelope.

### Shared item reference

- `ItemRef{Type, ID, Season *int, Episode *int}` (`internal/tools/itemref.go`) is embedded in
  each merged tool's argument struct. `type` and `id` are required in the input schema; a call
  without `type` fails the SDK's schema validation before the handler runs.
- `ItemRef.check(allowed...)` validates, in order: `type` (`checkType`, exact match),
  `id` (`invalid argument: id`), `season`, `episode`. A season or episode missing for its type,
  or given for a type that does not take it, fails with that field's name.
- `id` errors name `id`, not the legacy `movie_id` or `show_id`.
- Arguments that apply to some types only are refused for the others, never ignored:
  `get_summary` refuses `extended` with `type: season` or `episode`.
