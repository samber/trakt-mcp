# Tool Consolidation Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Cut the registry from 143 to 90 tools by merging per-media-type duplicates behind a `type` argument and replacing the `fetch_` prefix with `get_`/`list_`.

**Architecture:** Each merged tool is a thin dispatcher: it validates `type`, `id`, `season`, `episode`, builds the legacy argument struct and calls the existing handler. Existing handlers, trimming and paging stay untouched; only registrations, names and the argument layer change. The existing `catalogCase` tables keep their routes, fixtures and expected values, so passing tests prove behavior is preserved.

**Tech Stack:** Go 1.26, `modelcontextprotocol/go-sdk`, `google/jsonschema-go`, oapi-codegen client (untouched).

**Spec:** `docs/superpowers/specs/2026-11-25-tool-consolidation-design.md`

## Global Constraints

- Branch name `refactor/tool-consolidation`, cut from freshly fetched `main`. No "worktree" or "claude" in the name.
- Run before every commit: `gofmt -l .` (prints nothing), `go vet ./...`, `make lint`, `make test`.
- `git add` explicit paths only. `internal/oauth/flow.go` and `internal/oauth/flow_test.go` carry unrelated uncommitted edits: never stage them.
- Conventional Commits, lowercase, imperative, subject at most 72 characters. No `Claude-Session` line and no mention of Claude in commits or PR text.
- No magic strings: `type` values, kinds and field names are named constants, each with a one-line "why" comment.
- Comments describe current code only. Never mention previous names or the refactor.
- Every tool keeps `title`, explicit `readOnlyHint`, `destructiveHint`, `openWorldHint` (`TestToolAnnotations`).
- Tool descriptions of anything accepting `type: show`, `season` or `episode` must say "TV show" or "TV series".
- Never log tool arguments or results above `debug`. Branch on `*trakt.Error` `Kind`, never on its message.
- Keep tool-name tests honest: renaming a tool name constant in a test is allowed, deleting a case or an assertion is not.
- Count of registered tools is asserted in one test only (`TestRegistryHas143Tools`, `internal/tools/items_test.go:635`).

## Review Focus

- `type: episode` without `season` or `episode`: expect `invalid argument: season` or `invalid argument: episode`, never a Trakt call.
- `type` empty, upper-case (`Movie`) or outside the tool's accepted values: expect `invalid argument: type`.
- `season` or `episode` supplied with `type: movie` or `type: show`: expect `invalid argument: season` (rejected, not ignored).
- Trakt 404 on a merged tool: expect the same `not found` tool error as the legacy tool, not an internal error.
- A removed legacy name (`fetch_movie_summary`) called by an old client: expect an unknown-tool error, and no registry entry.

---

## File Structure

- Create `internal/tools/itemref.go`: `ItemRef`, type constants, validation, shared by every merged tool.
- Create `internal/tools/itemref_test.go`: validation matrix.
- Modify area files (`movies.go`, `shows.go`, `seasons.go`, `episodes.go`, `comments.go`, `people.go`, `watchnow.go`, `recommendations.go`, `checkin.go`, `search_extra.go`, `calendars.go`, `lists.go`, `movie_extras.go`, `show_extras.go`, and the rest for rename): registrations only.
- Modify `registry_test.go`, `items_test.go`, area `_test.go` files: tool names and args.
- Modify docs and listings in Task 8.

---

### Task 1: Shared item reference and `get_summary` (spike)

**Files:**

- Create: `internal/tools/itemref.go`, `internal/tools/itemref_test.go`
- Modify: `internal/tools/movies.go`, `shows.go`, `seasons.go`, `episodes.go` (registrations of the four summary tools), `registry_test.go` (`accessByPrefix`, show-description test), `docs/superpowers/specs/2026-11-25-tool-consolidation-design.md` (record decision)
- Test: `internal/tools/itemref_test.go`, the movie, show, season and episode case tables

**Interfaces:**

- Produces:
  - `type ItemRef struct { Type string; ID string; Season *int; Episode *int }`, JSON `type`, `id`, `season,omitempty`, `episode,omitempty`, with `jsonschema` descriptions.
  - Constants `typeMovie`, `typeShow`, `typeSeason`, `typeEpisode` (`"movie"`, `"show"`, `"season"`, `"episode"`).
  - `func (r ItemRef) check(allowed ...string) error`: returns `invalidArgument("type")` for a value not in `allowed`, `invalidArgument("season")` or `invalidArgument("episode")` for a missing or extra field.
  - `func (r ItemRef) movieArgs() MovieSummaryArgs`, `showRef() ShowRef`, `seasonRef() SeasonRef`, `episodeRef() EpisodeRef` (use the real legacy ref types).
  - `Typed` output envelope or union, per the spike result below.
  - `accessByPrefix` entries `"get_": Read`, `"list_": Read`, `"search": Read`, `"checkin": Write`.

- [ ] **Step 1: Create the branch.** `git fetch origin main`, then `git switch -c refactor/tool-consolidation origin/main`. The spec and this plan already sit on `main` or in the docs PR; if that PR is unmerged, branch from `docs/tool-consolidation-plan` instead.

- [ ] **Step 2: Write the failing validation test** `TestItemRefCheck` in `itemref_test.go`, table-driven, cases: movie ok; show ok; season with `season: 1` ok; season without season fails `season`; episode with season only fails `episode`; episode with both ok; movie with `season: 1` fails `season`; type `""`, `"Movie"`, `"person"` fail `type`; type `show` passed to `check(typeMovie)` fails `type`; season `-1` and `1000` fail `season`; episode `0` and `10000` fail `episode`. Assert on the error text `invalid argument: <field>`.

- [ ] **Step 3: Run** `go test ./internal/tools -run TestItemRefCheck -v`. Expected: FAIL, `ItemRef` undefined.

- [ ] **Step 4: Implement `ItemRef` and `check`** in `itemref.go`, reusing `checkSeason`/`checkEpisode` and `idArg`. Season bounds come from the existing constants.

- [ ] **Step 5: Spike the output shape.** Register `get_summary` (`Access: Read`) in a new `registerSummaryTools` appended to `catalog`, dispatching to the four legacy summary handlers. Try first the envelope `type SummaryResult struct { Type string; Item any }`. Run `TestToolAnnotations` and a call through `harness.ok`. If the go-sdk rejects the `any` output schema or output validation fails, try a `oneOf` schema built with `jsonschema.Schema`. Keep the simplest shape that passes `requireSerializedOutput`. Write the decision and the reason under "Decisions" in the spec.

- [ ] **Step 6: Port the four summary cases.** In the movie, show, season and episode case tables change `tool` to `toolGetSummary` and add `type`, `id`, `season`, `episode` args. Routes, fixtures and `want` stay as they are, with the `Item.` path prefix if the envelope is chosen.

- [ ] **Step 7: Remove the four legacy `Register` calls** (keep the handlers, now called by the dispatcher). Update `TestShowToolDescriptionsSayTVShow` so its predicate also selects any spec whose input schema has a `type` property. Add the four prefixes above to `accessByPrefix`.

- [ ] **Step 8: Run the gates.** Expected: all pass, `TestRegistryHas143Tools` now fails with 140. Change that test's name and constant to `TestRegistryHas140Tools`/140 in this commit.

- [ ] **Step 9: Commit** `refactor(tools): add get_summary merging four summary tools`.

### Task 2: Merge ratings, stats, watching, translations

**Files:**

- Modify: `movies.go`, `movie_extras.go`, `shows.go`, `show_extras.go`, `seasons.go`, `episodes.go`, `ratings.go` and their `_test.go` case tables.

**Interfaces:**

- Consumes: `ItemRef` and `check` from Task 1, the envelope or union decided there.
- Produces: `get_ratings`, `get_stats`, `list_watching`, `list_translations`; each accepts `movie`, `show`, `season`, `episode`. `list_translations` also takes `language`. The user-rating tool `fetch_ratings` is untouched until Task 7.

- [ ] **Step 1: Port the 16 case-table rows** to the four new tool names, adding `type`/`id`/`season`/`episode`. Add one failing case per tool for a missing `season` on `type: season`, asserting `invalid argument: season`.
- [ ] **Step 2: Run** `go test ./internal/tools -run 'TestCatalog' -v`. Expected: FAIL, tools unknown.
- [ ] **Step 3: Implement the four dispatchers** `getRatings`, `getStats`, `listWatching`, `listTranslations`, one per file area, each calling the matching legacy handler through `ItemRef`.
- [ ] **Step 4: Remove the 16 legacy `Register` calls.** Run the gates; set the count test to 128.
- [ ] **Step 5: Commit** `refactor(tools): merge ratings, stats, watching and translations by type`.

### Task 3: Merge comments, people, videos, item lists

**Files:**

- Modify: `comments.go`, `people.go`, `movie_extras.go`, `show_extras.go`, `seasons.go`, `episodes.go`, and their tests.

**Interfaces:**

- Produces: `list_comments` (`sort`, `limit`, `page`), `list_people`, `list_videos`, `list_item_lists` (`list_type`, `sort`, `limit`, `page`; description carries `untrustedNote`).
- `untrustedTextTools` in `registry_test.go` replaces the four `fetch_*_lists` names by `list_item_lists`.

- [ ] **Step 1: Port the 16 rows**; add the missing-`season` failure case per tool; keep the paging headers and `wantQuery` assertions.
- [ ] **Step 2: Run** the catalog tests; expected FAIL.
- [ ] **Step 3: Implement the four dispatchers**, embedding `PageArgs` where the legacy args do.
- [ ] **Step 4: Remove 16 legacy registrations**, update `untrustedTextTools`, set the count to 116, run the gates.
- [ ] **Step 5: Commit** `refactor(tools): merge comments, people, videos and item lists by type`.

### Task 4: Merge movie/show pairs (watchnow, aliases, studios, recommendations, related)

**Files:**

- Modify: `watchnow.go`, `movie_extras.go`, `show_extras.go`, `recommendations.go`, `movies.go`, `shows.go`, and their tests.

**Interfaces:**

- Produces: `get_watchnow` (`movie`, `show`, `episode`; takes `country`), `list_aliases`, `list_studios`, `list_recommendations`, `list_related` (`movie`, `show`), `hide_recommendation`, `unhide_recommendation` (`Write`).

- [ ] **Step 1: Port the 18 rows**; add a failing case per tool passing `type: season`, asserting `invalid argument: type`.
- [ ] **Step 2: Run** catalog tests; expected FAIL.
- [ ] **Step 3: Implement the dispatchers** with `check(typeMovie, typeShow)` (and `typeEpisode` for `get_watchnow`).
- [ ] **Step 4: Remove 18 legacy registrations**, set the count to 108, run the gates.
- [ ] **Step 5: Commit** `refactor(tools): merge movie and show recommendation, studio and watchnow tools`.

### Task 5: Merge rankings, person credits, checkin, search

**Files:**

- Modify: `movies.go`, `shows.go`, `movie_extras.go`, `show_extras.go`, `people.go`, `checkin.go`, `search_extra.go`, and their tests.

**Interfaces:**

- Produces: `list_trending`, `list_popular`, `list_anticipated`, `list_favorited` (`period`), `list_played` (`period`), `list_watched` (`period`), each over `movie`, `show`; `list_person_credits` (`id` is the person, `type` movie or show); `checkin` (`Write`, `type` movie or show); `search` (`type`, `query`, `exact` bool, `limit`, `page`). `list_boxoffice` stays movie-only and is renamed in Task 7.
- These tools take a bare `type` with no `ItemRef.ID`: define `TypeArgs`-style structs embedding `PageArgs`, reusing the type constants and a shared `checkType(value string, allowed ...string) error`.

- [ ] **Step 1: Port the 22 rows**; add `search` cases for `exact: true` and for `type: person` failing `invalid argument: type`.
- [ ] **Step 2: Run** catalog tests; expected FAIL.
- [ ] **Step 3: Implement the dispatchers.** Keep `checkin` in the per-user write lock path (`Access: Write`).
- [ ] **Step 4: Remove 22 legacy registrations**, set the count to 97, run the gates.
- [ ] **Step 5: Commit** `refactor(tools): merge rankings, person credits, checkin and search by type`.

### Task 6: Merge calendars and list rankings

**Files:**

- Modify: `calendars.go`, `lists.go`, and their tests.

**Interfaces:**

- Produces: `list_calendar` with `kind` in `shows`, `new_shows`, `premieres`, `finales`, `movies`, `dvd`, `streaming` plus the legacy date arguments; `list_lists` with `kind` in `trending`, `popular`. Kinds are named constants; an unknown kind returns `invalid argument: kind`.

- [ ] **Step 1: Port the 9 rows**; add an unknown-`kind` failing case per tool.
- [ ] **Step 2: Run** catalog tests; expected FAIL.
- [ ] **Step 3: Implement both dispatchers** over a `kind` to legacy handler map.
- [ ] **Step 4: Remove 9 legacy registrations**, set the count to 90, run the gates.
- [ ] **Step 5: Commit** `refactor(tools): merge calendar and list ranking tools by kind`.

### Task 7: Rename remaining `fetch_` tools

**Files:**

- Modify: every area file still registering `fetch_*`, `registry_test.go`, area tests, `items_test.go`.

**Interfaces:**

- Produces: the rename table of the spec ("Renamed only"). `accessByPrefix` loses `fetch_`.
- Consumes: spec mapping; `untrustedTextTools` gets the new names (`list_user_lists`, `get_list`, `list_list_items`, `get_note`, `list_user_notes`, `list_favorites`, `list_movie_releases`).

- [ ] **Step 1: Change the tool-name constants in tests first**, run `go test ./internal/tools`; expected FAIL on unknown tools.
- [ ] **Step 2: Rename registrations, titles and doc comments** (`fetch_x` to the new name; titles `Fetch ...` to `Get ...`/`List ...`).
- [ ] **Step 3: Remove `fetch_` from `accessByPrefix`.** Add a test `TestNoFetchPrefix` asserting no spec name starts with `fetch_`.
- [ ] **Step 4: Run the gates**; the count stays 90.
- [ ] **Step 5: Commit** `refactor(tools): replace fetch_ prefix with get_ and list_`.

### Task 8: Docs, plugin, site

**Files:**

- Modify: `README.md` (tool tables), `site/src/data/site.ts`, `site/src/components/Features.astro`, `docs/directory-submission.md`, `docs/superpowers/specs/2026-11-23-trakt-mcp-design.md` (tool table), `CLAUDE.md` (`TestRegistryHas90Tools`), `plugin/openai/plugin.json` (bump `version`).

- [ ] **Step 1: Grep every old name** with `/usr/bin/grep -rnE 'fetch_[a-z_]+' README.md site/src docs plugin CLAUDE.md` and replace per the spec mapping. Expected: no `fetch_` left.
- [ ] **Step 2: Bump `version`** in `plugin/openai/plugin.json`, run `make plugin-openai`.
- [ ] **Step 3: Rename the count test** to `TestRegistryHas90Tools` if not done, and reference it in `CLAUDE.md`.
- [ ] **Step 4: Run** the site gates in `site/CLAUDE.md` and all Go gates.
- [ ] **Step 5: Commit** `docs: update tool names for consolidated registry`.

### Task 9: Whole-branch review

- [ ] **Step 1: Run** the full gate list, plus `go generate ./internal/trakt/api && git diff --exit-code internal/trakt`.
- [ ] **Step 2: Mutation check:** change one dispatcher to ignore `season`; expect its failing case to turn red; revert.
- [ ] **Step 3: Open a draft PR**, no test-plan checklist, listing old to new mapping and the hard-break note.

---

## Self-review

- Spec coverage: naming scheme (Tasks 1, 7), every mapping table section (Tasks 1 to 7), output-shape question (Task 1 spike), compatibility (Review Focus, Task 9 PR note), files touched (Task 8). Evals (spec question 4) are not in this plan: decide separately after the PR.
- Count chain: 143, 140, 128, 116, 108, 97, 90.
- Open risk: if the spike finds no workable output schema for heterogeneous types, Tasks 1 to 4 need a rethink; stop and report.
