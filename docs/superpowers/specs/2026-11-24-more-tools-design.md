# More Trakt tools: design

## Intent

Cover the Trakt API areas users ask an assistant about and the server does not expose yet: calendars, where to watch, search by id, per-item extras, custom lists, watchlist edits, notes, show progress and reference data.

## Goals

- 64 new tools, from 79 to 143, in one PR.
- Follow the existing convention: movie and show tools stay separate; a path `{type}` that Trakt itself shares becomes one `type` argument.
- Every tool goes through `Register`, with title, description and explicit annotations.

## Non-goals

- Last activities, `up_next_nitro`, `GET /sync/progress/watched` across all shows, comments write, social (follow, block, profiles), year in review, scrobble, OAuth device flow, sync/Plex/Younify, avatar and cover, reports.
- Hot calendars (`/calendars/hot/*`), `/calendars/{target}/media`, `watchnow/justwatch_links`, `sentiments`, `awards`, `lists/collaborations`, list/item reorder and like, list comments.

## Constraints

- One SQLite file, no Trakt data on disk. Writes take the per-user write lock; Destroy tools set `destructiveHint`.
- Notes need Trakt VIP; the VIP error is already mapped (`KindVIPRequired`).
- `endpoints.txt` + overlay drive codegen. Run `make generate`, commit `openapi.filtered.json` and `api/api.gen.go` together.
- Bump `TestRegistryHas79Tools` to 143 (rename `TestRegistryHas143Tools`), `plugin/openai/plugin.json` version, run `make plugin-openai`, sync README, plugin and site tool lists.
- Comments, notes and list text from other users are untrusted: use `boundText` and `untrustedNote`.

## Approaches

1. **One tool per endpoint** (about 85 tools): simple schemas, too many tools.
2. **Merge by `kind`/`type` argument** (about 30 tools): fewer tools, diverges from today's code.
3. **Existing convention** (chosen by the owner): movie/show split, merge only where Trakt's own path takes `{type}`.

## Tool list (64)

| Area | Tools | Access |
|---|---|---|
| Calendar (7). Argument `scope`: `my` or `all` | `fetch_calendar_shows`, `fetch_calendar_new_shows`, `fetch_calendar_show_premieres`, `fetch_calendar_show_finales`, `fetch_calendar_movies`, `fetch_calendar_dvd`, `fetch_calendar_streaming` | Read |
| Where to watch (4) | `fetch_movie_watchnow`, `fetch_show_watchnow`, `fetch_episode_watchnow` (country), `fetch_watchnow_sources` | Read |
| Search (3) | `search_by_id`, `search_movies_exact`, `search_shows_exact` | Read |
| Movie extras (7) | `fetch_movie_stats`, `_watching`, `_translations`, `_aliases`, `_studios`, `_lists`, `_releases` | Read |
| Show extras (9) | `fetch_show_stats`, `_watching`, `_translations`, `_aliases`, `_studios`, `_lists`, `_next_episode`, `_last_episode`, `_certifications` | Read |
| Custom lists read (5) | `fetch_user_lists`, `fetch_list`, `fetch_list_items`, `fetch_trending_lists`, `fetch_popular_lists` | Read |
| Custom lists write (5) | `create_list`, `update_list`, `add_list_items`, `remove_list_items` (Destroy), `delete_list` (Destroy) | Write/Destroy |
| Watchlist writes (2) | `update_watchlist_item`, `reorder_watchlist` | Write |
| Notes (5) | `fetch_note`, `fetch_user_notes`, `create_note`, `update_note` (Write), `delete_note` (Destroy) | Read/Write/Destroy |
| Progress (3) | `fetch_show_collection_progress` (Read), `reset_show_progress` (Write), `undo_reset_show_progress` (Write) | Mixed |
| Favorites (5): the user's own top list, max 50 items, optional note per item | `fetch_favorites`, `add_to_favorites`, `update_favorite` (note and rank), `reorder_favorites`, `remove_from_favorites` (Destroy) | Read/Write/Destroy |
| Up next and hidden progress (4) | `fetch_up_next` (Read), `fetch_hidden_progress` (Read), `hide_from_progress` (Write), `unhide_from_progress` (Write) | Mixed |
| Reference data (5) | `fetch_genres`, `fetch_countries`, `fetch_languages`, `fetch_certifications`, `fetch_networks` | Read |

Reference-data tools take a `type` argument (`movies` or `shows`) where Trakt does. `fetch_networks` takes none.

## Left to the builder

- Argument names and descriptions, following neighbouring tool files.
- Output types: reuse `Listing[T]`, `TraktList`, `Stats`, `Watching`, `Translation`; add new ones only when no existing type fits.
- Test files per area: every error kind of the Trakt call (404, 401, 420, 423, 426, 429, 5xx, malformed body, paging), pivot values (empty id, season 0, `limit` bounds).
- One commit per area inside the single PR.

## Calls made without the owner

- `reset_show_progress` is Write, not Destroy: Trakt keeps the history and `undo` reverses it.
- Every `remove_*` and `delete_*` tool is Destroy, as in the existing tools (`remove_list_items` included).
- Notes are "VIP Enhanced": free accounts keep a few notes and Trakt answers 420 past the cap; watchlist and list-item notes and show progress reset need VIP.
- Notes tools stay in despite VIP: the error message already says so.
- "Progress" read as show collection progress plus reset/undo; up next is out.
