# More Trakt tools Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add 64 tools (79 to 143) for calendars, where to watch, search by id, per-item extras, custom lists, watchlist edits, notes, favorites, show progress, up next, hidden progress and reference data.

**Architecture:** One new file per area in `internal/tools/`, each with a `register<Area>Tools(s *Server)` group added to `catalog`. Generated client methods come from adding the endpoints to `internal/trakt/endpoints.txt`. Task 0 does all shared edits so Tasks 1 to 10 touch disjoint files and can run in parallel.

**Tech Stack:** Go 1.26, go-sdk MCP, oapi-codegen client (`internal/trakt/api`), testify-free table harness in `internal/tools/*_test.go` (`catalogCase`, `argCase`).

**Spec:** `docs/superpowers/specs/2026-11-24-more-tools-design.md`

## Global Constraints

- Branch `feat/more-tools`, one draft PR, Conventional Commits, one commit per task. No "claude" or "worktree" in branch or commit text.
- Every tool: `Register(s, Spec{Name, Title, Description, Access}, handler)`. Names, titles and access exactly as in the spec table. Write tools end with the standard warning (added by `Register`).
- Destroy access only for `delete_list`, `delete_note`. `reset_show_progress` and `undo_reset_show_progress` are Write.
- No magic numbers or strings inline: named constants with a one-line "why" comment. Never ignore errors. Never log tool arguments or results above debug.
- Third-party text (list names and descriptions, notes) goes through `boundText`; descriptions of tools returning such text end with `untrustedNote`.
- IDs go through `idArg(field, s, kinds)`; enum arguments through `oneOf(field, value, default, allowed)`; paging through `PageArgs` + `paginateDecoded`; plain GETs through `fetchParsed`; item writes through `writeItems`.
- Every description of a tool about shows, seasons or episodes says they are TV shows (series), not movies, so the model reads "show" correctly: e.g. "Get a TV show's ..." / "List TV series ...". Applies to the new tools and, in Task 12, to the existing ones.
- Never edit `internal/trakt/api/` by hand. Run `make generate` after touching `endpoints.txt`.
- Gates before every commit: `gofmt -l .` (prints nothing), `go vet ./...`, `make lint`, `make test`.

## Review Focus

- A list tool called with `limit` 0, 100 and 101, and `page` 0: defaults, accepted, `invalid argument: limit`, `invalid argument: page`.
- An id argument holding `../x`, an empty string, uppercase or a 129-character string: `invalid argument: <field>`, no request sent.
- A Trakt 404 on a private or missing list, note or show: `not found`, not `internal error`.
- A Trakt 420 on list creation and 426 on notes: `Trakt account limit reached; do not retry` and `Trakt VIP is required...`.
- A `days` or `start_date` value outside what Trakt accepts: rejected locally, no request sent.

## File Structure

| File | Responsibility |
|---|---|
| `internal/trakt/endpoints.txt`, `openapi.filtered.json`, `api/api.gen.go` | Task 0 only |
| `internal/tools/calendars.go` | Task 1 |
| `internal/tools/watchnow.go`, `search_extra.go` | Task 2 |
| `internal/tools/movie_extras.go` | Task 3 |
| `internal/tools/show_extras.go` | Task 4 |
| `internal/tools/lists.go` (read), `lists_write.go` | Tasks 5, 6 |
| `internal/tools/watchlist_edit.go` | Task 7 |
| `internal/tools/notes.go` | Task 8 |
| `internal/tools/progress.go` | Task 9 |
| `internal/tools/reference.go` | Task 10 |
| `internal/tools/favorites.go` | Task 13 |
| `internal/tools/up_next.go` (up next and hidden progress) | Task 14 |
| `catalog.go`, `items_test.go`, README, `plugin/openai/*`, `site/` lists | Tasks 0 and 11 |

Each `*.go` file has a `*_test.go` twin that registers its cases with `func init() { catalogCaseSets = append(catalogCaseSets, <area>Cases) }`, as `episodes_test.go` does. Fixtures go in `internal/tools/testdata/trakt/<name>.json` (copy real shapes from `openapi.filtered.json` examples).

---

### Task 0: Endpoints, generated client, empty groups

**Files:**
- Modify: `internal/trakt/endpoints.txt`, `internal/trakt/openapi.filtered.json`, `internal/trakt/api/api.gen.go`, `internal/tools/catalog.go`
- Create: twelve stub files listed in File Structure (Tasks 1 to 10, 13, 14), each with only `package tools` and an empty `func register<Area>Tools(s *Server) {}`.

**Interfaces:**
- Produces: group functions `registerCalendarTools`, `registerWatchNowTools`, `registerSearchExtraTools`, `registerMovieExtraTools`, `registerShowExtraTools`, `registerListTools` (read and write both live here, Task 6 adds to it), `registerWatchlistEditTools`, `registerNoteTools`, `registerProgressTools`, `registerReferenceTools`, `registerFavoriteTools`, `registerUpNextTools`; and the generated methods named by oapi-codegen from the paths below.

- [ ] **Step 1:** Append these lines to `endpoints.txt` (keep spec spelling, trailing slashes included):

```
GET /calendars/{target}/shows/{start_date}/{days}
GET /calendars/{target}/shows/new/{start_date}/{days}
GET /calendars/{target}/shows/premieres/{start_date}/{days}
GET /calendars/{target}/shows/finales/{start_date}/{days}
GET /calendars/{target}/movies/{start_date}/{days}
GET /calendars/{target}/dvd/{start_date}/{days}
GET /calendars/{target}/streaming/{start_date}/{days}
GET /movies/{id}/watchnow/{country}
GET /shows/{id}/watchnow/{country}
GET /shows/{id}/seasons/{season}/episodes/{episode}/watchnow/{country}
GET /watchnow/sources
GET /search/{id_type}/{id}
GET /search/{type}/exact
GET /movies/{id}/stats
GET /movies/{id}/watching
GET /movies/{id}/translations
GET /movies/{id}/aliases
GET /movies/{id}/studios
GET /movies/{id}/lists/{type}/{sort}
GET /movies/{id}/releases/{country}
GET /shows/{id}/stats
GET /shows/{id}/watching
GET /shows/{id}/translations
GET /shows/{id}/aliases
GET /shows/{id}/studios
GET /shows/{id}/lists/{type}/{sort}
GET /shows/{id}/next_episode
GET /shows/{id}/last_episode
GET /shows/{id}/certifications
GET /users/{id}/lists
GET /users/{id}/lists/{list_id}/
GET /users/{id}/lists/{list_id}/items/{type}/{sort_by}/{sort_how}
GET /lists/trending
GET /lists/trending/{type}
GET /lists/popular
GET /lists/popular/{type}
GET /lists/{id}
GET /lists/{id}/items/{type}/{sort_by}/{sort_how}
POST /users/{id}/lists
PUT /users/{id}/lists/{list_id}/
DELETE /users/{id}/lists/{list_id}/
POST /users/{id}/lists/{list_id}/items
POST /users/{id}/lists/{list_id}/items/remove
PUT /sync/watchlist/{list_item_id}
POST /sync/watchlist/reorder
GET /notes/{id}
GET /users/{id}/notes/{type}
POST /notes
PUT /notes/{id}
DELETE /notes/{id}
GET /shows/{id}/progress/collection
POST /shows/{id}/progress/watched/reset
DELETE /shows/{id}/progress/watched/reset
GET /genres/{type}
GET /countries/{type}
GET /languages/{type}
GET /certifications/{type}
GET /networks
GET /sync/favorites/{type}/{sort_by}/{sort_how}
POST /sync/favorites
POST /sync/favorites/remove
POST /sync/favorites/reorder
PUT /sync/favorites/{list_item_id}
GET /sync/progress/up_next
GET /users/hidden/progress_watched
POST /users/hidden/{section}
```

- [ ] **Step 2:** Run `make spec-fetch && make generate`. Expected: exit 0; `git diff --stat internal/trakt` shows the three generated files. If `filterspec` rejects a path, fix its spelling against the spec (do not edit the spec).
- [ ] **Step 3:** Create the twelve stub files and append the twelve group functions to `catalog` in `catalog.go`.
- [ ] **Step 4:** Run `go build ./... && make test`. Expected: PASS (`TestRegistryHas79Tools` still 79).
- [ ] **Step 5:** `git add internal/trakt internal/tools && git commit -m "feat(trakt): add endpoints for calendars, lists, notes and extras"`.

---

### Task 1: Calendar (7 tools)

**Files:** Modify `internal/tools/calendars.go`; Test `internal/tools/calendars_test.go`.

**Interfaces:**
- Consumes: `oneOf`, `fetchParsed`, `Listing`, `api.GetCalendars...` generated methods (names from `api.gen.go`).
- Produces: `CalendarArgs{Scope string; StartDate string; Days int}` with json `scope` ("my" or "all", default "my"), `start_date` (YYYY-MM-DD, default today UTC), `days` (1 to the max Trakt allows, default 7).

- [ ] **Step 1:** Write cases in `calendars_test.go` for each tool: route `GET /calendars/my/shows/2026-11-24/7` style, fixture of 2 entries, `want` asserting first entry fields. Add `TestCalendarToolArguments` with: `scope: "x"`, `start_date: "2026-13-01"`, `start_date: "24-11-2026"`, `days: 0`, `days` one above the max, `days` max; each invalid case expects `invalid(<field>)`, no request sent.
- [ ] **Step 2:** Run `go test ./internal/tools -run 'Calendar' -v`. Expected: FAIL (tools missing).
- [ ] **Step 3:** Implement `registerCalendarTools` with `fetch_calendar_shows`, `fetch_calendar_new_shows`, `fetch_calendar_show_premieres`, `fetch_calendar_show_finales`, `fetch_calendar_movies`, `fetch_calendar_dvd`, `fetch_calendar_streaming`. Output `Listing[CalendarEntry]` where `CalendarEntry{FirstAired/Released string; Episode *EpisodeBrief; Show/Movie ShowRef/MovieRef}`; the max `days` is a named constant with the spec's value.
- [ ] **Step 4:** Rerun Step 2 command. Expected: PASS. Run gates.
- [ ] **Step 5:** `git commit -m "feat(tools): add calendar tools"`.

### Task 2: Where to watch and search by id (7 tools)

**Files:** Modify `watchnow.go`, `search_extra.go`; Test `watchnow_test.go`, `search_extra_test.go`.

**Interfaces:**
- Consumes: `ShowRef`, `MovieRef`, `EpisodeRef`, `idArg`, `languageArg`-style country validation (add `countryArg(s string) (string, error)`: two lowercase letters, field `country`).
- Produces: `WatchNowArgs{Country string}` embedded in the ref structs of each tool; `SearchByIDArgs{IDType string; ID string; Type string}` (`id_type` one of `trakt`, `imdb`, `tmdb`, `tvdb`; `type` optional `movie|show|episode|person`).

- [ ] **Step 1:** Cases: `fetch_movie_watchnow` route `GET /movies/the-matrix/watchnow/fr`; `fetch_show_watchnow`; `fetch_episode_watchnow` (route under `/shows/{id}/seasons/1/episodes/2/watchnow/fr`); `fetch_watchnow_sources`; `search_by_id` route `GET /search/imdb/tt0133093` with `wantQuery` `type=movie`; `search_movies_exact` and `search_shows_exact` route `GET /search/movie/exact` with `wantQuery` `query`. Argument cases: country `FR`, `fra`, empty; `id_type: "x"`; `query` empty.
- [ ] **Step 2:** Run `go test ./internal/tools -run 'WatchNow|SearchBy|Exact' -v`. Expected: FAIL.
- [ ] **Step 3:** Implement the 7 tools (`fetch_movie_watchnow`, `fetch_show_watchnow`, `fetch_episode_watchnow`, `fetch_watchnow_sources`, `search_by_id`, `search_movies_exact`, `search_shows_exact`), Read. Outputs: provider groups (`free`, `ads`, `flatrate`, `rent`, `buy`) each a list of `{name, link}`; search outputs reuse the result type of `search_movies`/`search_shows`.
- [ ] **Step 4:** Rerun; PASS; gates.
- [ ] **Step 5:** `git commit -m "feat(tools): add where-to-watch and search-by-id tools"`.

### Task 3: Movie extras (7 tools)

**Files:** Modify `movie_extras.go`; Test `movie_extras_test.go`.

**Interfaces:**
- Consumes: `MovieRef` (`movies.go:233`), `Stats`/`statsOf`, `Watching`/`watchingOf`, `Translation`/`parseTranslations`, `listArgs`, `trimList`, `TraktList` (all in `episodes.go`/`seasons.go` patterns).
- Produces: `fetch_movie_stats`, `fetch_movie_watching`, `fetch_movie_translations` (optional `language`), `fetch_movie_aliases` (`Listing[Alias{Title, Country}]`), `fetch_movie_studios` (`Listing[Studio{Name, Country, Ids}]`), `fetch_movie_lists` (type, sort, paging, `untrustedNote`), `fetch_movie_releases` (`country` optional; `Listing[Release{Country, Certification, ReleaseDate, ReleaseType, Note}]`).

- [ ] **Step 1:** Cases mirror `episodeCases` (`want`, `absent` for fields that must not leak, e.g. Trakt-only internals). Argument cases: bad movie id, `country` `FRA`, `list_type` `x`, `sort` `x`.
- [ ] **Step 2:** `go test ./internal/tools -run 'MovieExtra|MovieStats|MovieReleases' -v`. Expected: FAIL.
- [ ] **Step 3:** Implement, reusing the existing output types; add `Alias`, `Studio`, `Release` only here.
- [ ] **Step 4:** Rerun; PASS; gates.
- [ ] **Step 5:** `git commit -m "feat(tools): add movie stats, watchers, translations, aliases, studios, lists and releases"`.

### Task 4: Show extras (9 tools)

**Files:** Modify `show_extras.go`; Test `show_extras_test.go`.

**Interfaces:**
- Consumes: `ShowRef` (`shows.go:426`) and the same helpers as Task 3; reuse `Alias`, `Studio` from Task 3 only after Task 3 merges, otherwise define `showAlias` types locally and unify in Task 11.
- Produces: `fetch_show_stats`, `fetch_show_watching`, `fetch_show_translations`, `fetch_show_aliases`, `fetch_show_studios`, `fetch_show_lists`, `fetch_show_next_episode` and `fetch_show_last_episode` (output `Episode`; Trakt returns 204 when none: return `{}` with no error, add a case), `fetch_show_certifications` (`Listing[Certification{Name, Slug, Description}]`).

- [ ] **Step 1:** Cases as in Task 3, plus the 204 case for next and last episode asserting an empty object and no error.
- [ ] **Step 2:** `go test ./internal/tools -run 'ShowExtra|ShowStats|NextEpisode' -v`. Expected: FAIL.
- [ ] **Step 3:** Implement.
- [ ] **Step 4:** Rerun; PASS; gates.
- [ ] **Step 5:** `git commit -m "feat(tools): add show stats, watchers, translations, aliases, studios, lists, episodes and certifications"`.

### Task 5: Custom lists, read (5 tools)

**Files:** Modify `lists.go`; Test `lists_test.go`.

**Interfaces:**
- Consumes: `PageArgs`, `paginateDecoded`, `boundText`, `TraktList`/`trimList`, `MediaRef`/`WatchlistItem` patterns, `oneOf`.
- Produces: `fetch_user_lists` (`user` default `me`), `fetch_list` (`list_id`, optional `user`: with `user` call `/users/{id}/lists/{list_id}/`, without call `/lists/{id}`), `fetch_list_items` (same user switch; `type` all|movies|shows|seasons|episodes|people, `sort`, `sort_how`, paging), `fetch_trending_lists`, `fetch_popular_lists` (optional `type` `personal|official`, paging). List name and description bounded; `untrustedNote` in each description.

- [ ] **Step 1:** Cases for each tool and both path forms of `fetch_list` and `fetch_list_items`. Argument cases: `user` `../x`, `list_id` empty, `type` `x`.
- [ ] **Step 2:** `go test ./internal/tools -run 'List' -v`. Expected: FAIL.
- [ ] **Step 3:** Implement. User ids accept `Slug|Numeric`; list ids accept `Slug|Numeric`.
- [ ] **Step 4:** Rerun; PASS; gates.
- [ ] **Step 5:** `git commit -m "feat(tools): add custom list read tools"`.

### Task 6: Custom lists, write (5 tools)

**Files:** Modify `lists_write.go`; Test `lists_write_test.go`. Depends on Task 5 types (`TraktList`).

**Interfaces:**
- Consumes: `ItemArg`, `parseItems`, `writeItems`, `WriteResult` (`items.go`), `maxNotesRunes`-style constants.
- Produces: `create_list` (`name` required, `description`, `privacy` `private|friends|public`, `display_numbers`, `allow_comments`, `sort_by`, `sort_how`), `update_list` (same fields optional, plus `list_id`), `add_list_items` (`list_id`, `items` 1 to 50, same `Items` shape as `add_to_watchlist`), `remove_list_items` (Write), `delete_list` (Destroy). `user` fixed to `me`.

- [ ] **Step 1:** Cases for create (assert request body fields), update, add, remove, delete (204). Argument cases: empty `name`, `name` over Trakt's limit (constant), `privacy` `x`, 51 items, 0 items; Trakt 420 on create maps to the account-limit message; delete of a missing list maps to `not found`.
- [ ] **Step 2:** `go test ./internal/tools -run 'CreateList|UpdateList|ListItems|DeleteList' -v`. Expected: FAIL.
- [ ] **Step 3:** Implement. `delete_list` description states that it is irreversible.
- [ ] **Step 4:** Rerun; PASS; gates.
- [ ] **Step 5:** `git commit -m "feat(tools): add custom list write tools"`.

### Task 7: Watchlist edits (2 tools)

**Files:** Modify `watchlist_edit.go`; Test `watchlist_edit_test.go`.

**Interfaces:**
- Consumes: `maxNotesRunes`, `fieldNotes`, `writeItems` patterns from `watchlist.go`.
- Produces: `update_watchlist_item` (`list_item_id` int, `notes` string up to 500 runes; VIP), `reorder_watchlist` (`rank` list of list item ids, 1 to a named maximum).

- [ ] **Step 1:** Cases: update body `{"notes": "..."}`, reorder body `{"rank":[3,1,2]}`. Argument cases: notes 500 and 501 runes, invalid UTF-8, `list_item_id` 0 and -1, empty `rank`, duplicate ids in `rank`; Trakt 426 maps to the VIP message.
- [ ] **Step 2:** `go test ./internal/tools -run 'WatchlistItem|ReorderWatchlist' -v`. Expected: FAIL.
- [ ] **Step 3:** Implement, both Write.
- [ ] **Step 4:** Rerun; PASS; gates.
- [ ] **Step 5:** `git commit -m "feat(tools): add watchlist item update and reorder"`.

### Task 8: Notes (5 tools)

**Files:** Modify `notes.go`; Test `notes_test.go`.

**Interfaces:**
- Produces: `fetch_note` (`note_id`), `fetch_user_notes` (`type`, paging; `user` fixed to `me`), `create_note` (`notes` up to 500 runes, `item` as `ItemArg`, `privacy` `private|friends|public`, `spoiler` bool), `update_note` (`note_id`, same optional fields), `delete_note` (Destroy). All note text bounded with `boundText`; read tools carry `untrustedNote`.

- [ ] **Step 1:** Cases for each tool; argument cases for notes 0, 500, 501 runes, `privacy` `x`, `note_id` 0 and -1; 426 maps to the VIP message.
- [ ] **Step 2:** `go test ./internal/tools -run 'Note' -v`. Expected: FAIL.
- [ ] **Step 3:** Implement.
- [ ] **Step 4:** Rerun; PASS; gates.
- [ ] **Step 5:** `git commit -m "feat(tools): add note tools"`.

### Task 9: Show progress (3 tools)

**Files:** Modify `progress.go`; Test `progress_test.go`.

**Interfaces:**
- Consumes: `ShowRef`, the type behind `fetch_show_progress` (`history.go`).
- Produces: `fetch_show_collection_progress` (Read, same `hidden`/`specials` flags as `fetch_show_progress` if the spec has them), `reset_show_progress` (Write; optional `reset_at` RFC 3339), `undo_reset_show_progress` (Write). Descriptions say watch history is kept and that both reset tools are Trakt VIP only (a 426 maps to the VIP message). `fetch_show_collection_progress` takes `hidden` (bool) and `last_activity` (`aired|collected`).

- [ ] **Step 1:** Cases for the three routes (`POST` and `DELETE` on `/shows/{id}/progress/watched/reset`). Argument cases: bad show id, `reset_at` `yesterday`, `reset_at` empty.
- [ ] **Step 2:** `go test ./internal/tools -run 'Progress' -v`. Expected: FAIL.
- [ ] **Step 3:** Implement.
- [ ] **Step 4:** Rerun; PASS; gates.
- [ ] **Step 5:** `git commit -m "feat(tools): add show collection progress and reset tools"`.

### Task 10: Reference data (5 tools)

**Files:** Modify `reference.go`; Test `reference_test.go`.

**Interfaces:**
- Produces: `fetch_genres`, `fetch_countries`, `fetch_languages`, `fetch_certifications` (each `type` `movies|shows`, default `movies`; `Listing[Reference{Name, Slug/Code}]`), `fetch_networks` (no arguments; `Listing[Network{Name, Country}]`). Unpaginated: return the full list, capped by a named maximum with the cap stated in the description.

- [ ] **Step 1:** Cases for each route; argument cases `type` `x`, empty (default applies).
- [ ] **Step 2:** `go test ./internal/tools -run 'Genres|Countries|Languages|Certifications|Networks' -v`. Expected: FAIL.
- [ ] **Step 3:** Implement.
- [ ] **Step 4:** Rerun; PASS; gates.
- [ ] **Step 5:** `git commit -m "feat(tools): add reference data tools"`.

### Task 13: Favorites (5 tools)

**Files:** Modify `favorites.go`; Test `favorites_test.go`.

**Interfaces:**
- Consumes: `ItemArg`, `parseItems`, `writeItems`, `WriteResult`, `WatchlistItem`/`trimWatchlistItem` pattern, `maxNotesRunes`, `fieldNotes`, `oneOf`, `PageArgs`, `paginateDecoded`.
- Produces: `fetch_favorites` (`type` all|movies|shows, `sort` rank|added|title|released|runtime|popularity|percentage|votes, `sort_how`, paging; items carry `rank`, `notes`, `list_item_id` the id `update_favorite` takes), `add_to_favorites` (`items` 1 to 50 movies or shows only, `notes` up to 500 runes applied to every item), `update_favorite` (`list_item_id` int, optional `notes`, optional `rank`; at least one required), `reorder_favorites` (`rank` ordered list of list item ids), `remove_from_favorites` (Destroy, `items` 1 to 50). Descriptions explain: the user's own top list, capped by Trakt (420 past the cap, mapped to the account-limit message), not a sync with other platforms. Item text bounded with `boundText`.

- [ ] **Step 1:** Cases for each tool (routes `GET /sync/favorites/all/rank/asc`, `POST /sync/favorites`, `POST /sync/favorites/remove`, `POST /sync/favorites/reorder`, `PUT /sync/favorites/7`). Argument cases: seasons or episodes in `items` rejected locally, 0 and 51 items, notes 500 and 501 runes, `update_favorite` with neither `notes` nor `rank`, `list_item_id` 0 and -1, empty or duplicate `rank`; a Trakt 420 on add maps to the account-limit message.
- [ ] **Step 2:** `go test ./internal/tools -run 'Favorite' -v`. Expected: FAIL.
- [ ] **Step 3:** Implement `registerFavoriteTools`.
- [ ] **Step 4:** Rerun; PASS; gates.
- [ ] **Step 5:** `git commit -m "feat(tools): add favorites tools"`.

### Task 14: Up next and hidden progress (4 tools)

**Files:** Modify `up_next.go`; Test `up_next_test.go`.

**Interfaces:**
- Consumes: `ShowRef`/`MediaRef`, `ItemArg`, `parseItems`, `writeItems`, `PageArgs`, `paginateDecoded`, `oneOf`.
- Produces: `fetch_up_next` (Read; TV shows with the next episode to watch; `sort_by`, `sort_how`, `include_stats` bool, paging; Trakt's page count is a placeholder, so the description says to keep paging until an empty page), `fetch_hidden_progress` (Read; TV shows or seasons hidden from watched progress, `type` optional, paging; route `GET /users/hidden/progress_watched`), `hide_from_progress` (Write; `items` 1 to 50, shows or seasons only; route `POST /users/hidden/progress_watched`), `unhide_from_progress` (Write; route `POST /users/hidden/progress_watched/remove`). Descriptions say shows are TV shows and that hiding only affects progress views, not history.

- [ ] **Step 1:** Cases for the four routes and one up-next case with the placeholder pagination headers asserting that auto-paging stops on an empty page, not on `page_count`. Argument cases: movies in `items` rejected locally, 0 and 51 items, `sort_by` `x`.
- [ ] **Step 2:** `go test ./internal/tools -run 'UpNext|HiddenProgress|FromProgress' -v`. Expected: FAIL.
- [ ] **Step 3:** Implement `registerUpNextTools`.
- [ ] **Step 4:** Rerun; PASS; gates.
- [ ] **Step 5:** `git commit -m "feat(tools): add up next and hidden progress tools"`.

### Task 12: "TV show" wording in existing show tools

**Files:** Modify the `Description` of every existing tool whose name contains `show`, `season` or `episode` (`shows.go`, `seasons.go`, `episodes.go`, `comments.go`, `recommendations.go`, `history.go`, `checkin.go` and any other hit of `/usr/bin/grep -nE 'Name: +"[a-z_]*(show|season|episode)' internal/tools/*.go`); Test `internal/tools/registry_test.go`.

- [ ] **Step 1:** Add `TestShowToolDescriptionsSayTVShow`: for every spec whose name contains `show`, `season` or `episode`, assert the description contains `TV show` or `TV series` (case-insensitive). Run it; Expected: FAIL listing the tools.
- [ ] **Step 2:** Edit each description minimally (e.g. "Get a TV show's details..."). Keep lengths close to current; do not reword anything else.
- [ ] **Step 3:** Rerun; Expected: PASS. Run all gates.
- [ ] **Step 4:** `git commit -m "docs(tools): say that shows are TV series in tool descriptions"`.

### Task 11: Counts, docs, plugin

**Files:** Modify `internal/tools/items_test.go` (`TestRegistryHas79Tools` → `TestRegistryHas143Tools`, comment and count), `README.md`, `plugin/openai/plugin.json` (bump `version`), `site/` tool lists, `docs/`, root `CLAUDE.md` if it states a count.

- [ ] **Step 1:** Run `go test ./internal/tools -run TestRegistry -v`; Expected: only the count test fails (143 expected vs actual). If the actual is not 143, a task registered the wrong number: fix the task, not the test.
- [ ] **Step 2:** Update the count test; run `/usr/bin/grep -rnE '79 tools|79 outils|\b79\b' README.md site docs plugin CLAUDE.md` and update each hit; add the new tools to README, plugin and site lists.
- [ ] **Step 3:** Bump `plugin/openai/plugin.json` `version`; run `make plugin-openai`.
- [ ] **Step 3b:** Update the website. Add every new tool to `TOOLS` in `site/src/data/site.ts` (the page count is its length; `scripts/check-dist.mjs` requires it to equal the registrations in `internal/tools`). Update the hard-coded count and the tool groups in `site/src/components/Features.astro`. Keep demo data generic (public titles, no personal history), per `site/CLAUDE.md`. Run the site gates listed in `site/CLAUDE.md` (build with `ASTRO_TELEMETRY_DISABLED=1`, then `check-dist.mjs`); `astro build` may fail at the Vercel `build:done` hook in the sandbox, in which case check `dist/client/`.
- [ ] **Step 3c:** Update `README.md`: the tool count, a section per new area (calendars, where to watch, search by id, per-item extras, custom lists, watchlist edits, notes, favorites, progress, up next, hidden progress, reference data), and mention that notes, watchlist notes and show reset need Trakt VIP. Run `npx prettier --write README.md` only if the file was already prettier-formatted (check `git diff --stat` stays small).
- [ ] **Step 4:** Run all gates, plus `go generate ./internal/trakt/api && git diff --exit-code internal/trakt`. Expected: all pass.
- [ ] **Step 5:** `git commit -m "docs: update tool counts and lists"`; open a draft PR with `gh pr create --draft --body-file`.
