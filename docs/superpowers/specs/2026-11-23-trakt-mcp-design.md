# trakt-mcp: Design

Date: 2026-11-23. Author: Samuel Berthe. Status: draft for review.

Module: `github.com/samber/trakt-mcp`. License: MIT. Code and docs in English.

## 1. Intent

Let anyone with a Trakt account connect Claude or ChatGPT to it, with nothing to install. The assistant browses Trakt's catalog (trending, popular, search, details, people, comments) and manages the user's own data: watchlist, watch history, collection, ratings, check-ins, recommendations and smart lists.

The project ships as an open-source Go binary that anyone can self-host. A public instance, operated by Samuel Berthe personally, runs at `https://trakt-mcp.samber.dev` from launch day (hostname is a placeholder until confirmed).

## 2. Goals, non-goals, anti-goals

Goals:

- 90 MCP tools covering the catalog reads and the user writes listed in section 8.
- Multi-tenant: one instance serves many Trakt accounts, isolated from each other.
- Two consent screens to connect (ours, then Trakt's), no configuration on the user side.
- Viewing history, watchlist, ratings and other Trakt data never touch disk.

Non-goals for v1 (tracked as GitHub issues labelled `enhancement`):

- Personal custom lists CRUD (`/users/me/lists`), notes, comment writing, likes, following and friends.
- Scrobble endpoints (media-center playback), favorites, calendars, Plex/Jellyfin integrations.
- Device-code login, Trakt VIP-only endpoints beyond the optional watchlist note.
- Horizontal scaling, admin UI (CLI only).

Anti-goals (failure even if it works):

- Watch history, ratings, tool inputs or tool outputs written to disk or to logs at the default log level.
- A consent page that lets a malicious client impersonate "Claude" without the user seeing the real redirect domain.
- Any Trakt token stored in clear, or any token we issue stored in clear.
- A concurrent token refresh that burns a single-use Trakt refresh token and silently logs the user out.
- One user starving the shared Trakt API rate budget of the application.

## 3. Constraints

- Go 1.26+ (several dependencies, among them `golang.org/x/oauth2` and `modernc.org/sqlite`, declare `go 1.26.0`). Single CGO-free binary.
- SQLite via `modernc.org/sqlite`, path from `--db-path`. Single writer, so exactly one instance. The file may sit on a mounted network volume: rollback journal, not WAL, exactly one process. No replication or backup ships with the project.
- Trakt requires an API application (client id, client secret, registered redirect URI) and sends the `trakt-api-key` and `trakt-api-version: 2` headers on every call. Trakt publishes an API use policy; section 17 lists the check to run before the public launch.
- Trakt refresh tokens are single-use: every refresh returns a new refresh token and invalidates the previous one. Trakt supports PKCE; `client_secret` is deprecated but accepted from a server.
- Trakt free accounts have list, watchlist and collection caps; VIP raises them. The server surfaces the limit errors verbatim and never tries to work around them.

## 4. Approaches considered

### 4.1 Authorization server toward MCP clients

**ory/fosite**, chosen by the user for security pedigree, with hand-written RFC 8414 metadata, CIMD, `iss` and `resource` binding. giantswarm/mcp-oauth stays a reference for the CIMD fetcher.

### 4.2 Trakt API client

| Option | Trade-off |
| --- | --- |
| Hand-written thin client | Small and easy to review, but about 80 tools mean about 60 endpoints to type by hand and keep in sync. |
| **Generated from `openapi.json` (picked)** | Trakt publishes an official OpenAPI 3 file (`https://developer.trakt.tv/openapi.json`, 311 paths). `oapi-codegen` generates typed requests and responses. Picked by the user. |
| Raw passthrough | Fastest, but bloated replies and a risk of logging raw bodies. Rejected. |

How the generated client is used:

- A script filters the official spec down to the endpoints used by the tools and writes `internal/trakt/openapi.filtered.json`. The filtered spec and the generated Go code are committed. `make generate` regenerates both; CI fails when the committed output is stale.
- The generated client is wrapped by `internal/trakt/client.go`, written by hand: required headers, `Authorization`, one HTTP client with timeouts, pagination headers (`X-Pagination-*`), the error mapping of section 8.2 and the rate limiter of section 9.
- Tool output is not the raw Trakt body. Each tool maps the generated types into a trimmed structure (title, year, ids, rating, counts, dates), which keeps replies short and keeps free-form text such as notes out of logs.
- The Trakt spec is loose about some response shapes. The builder verifies each used endpoint against recorded fixtures (section 15) and patches the filtered spec, not the generated code, when a shape is wrong.

## 5. Architecture

```
Claude / ChatGPT ──OAuth 2.1 + MCP──▶ trakt-mcp ──OAuth + Trakt API v2──▶ api.trakt.tv
                                          │
                                          └── SQLite (users, encrypted Trakt tokens,
                                               token signatures, clients)
```

The server is its own OAuth 2.1 authorization server toward MCP clients and a confidential OAuth client toward Trakt. It never forwards a client token to Trakt. Every tool call reads or writes Trakt directly; no Trakt data is cached on disk.

### 5.1 HTTP endpoints

| Path | Purpose |
| --- | --- |
| `POST /mcp` | MCP Streamable HTTP, stateless mode |
| `/.well-known/oauth-protected-resource` | RFC 9728 metadata (go-sdk `auth.ProtectedResourceMetadataHandler`) |
| `/.well-known/oauth-protected-resource/mcp` | Same document at the path-suffixed location |
| `/.well-known/oauth-authorization-server` | RFC 8414 metadata (hand-written) |
| `GET /oauth/authorize` | Start of flow; validates client, shows consent page |
| `POST /oauth/consent` | User approves; redirects to Trakt |
| `GET /oauth/trakt/callback` | Trakt returns; finishes our authorization response |
| `POST /oauth/token` | Code and refresh grants (form-encoded) |
| `POST /oauth/register` | RFC 7591 DCR, only when `--enable-dcr` |
| `POST /oauth/revoke` | RFC 7009 revocation |
| `GET /` | Redirect to `--site-url` (the Astro site renders home, privacy, terms) |
| `GET /.well-known/openai-apps-challenge` | Only with `--openai-apps-challenge` |
| `--metrics-path` | Prometheus, only when `--metrics`, basic auth |

Unknown paths, and the metrics path when disabled, return the same 404.

### 5.2 MCP transport

- go-sdk v1.8.0, `StreamableHTTPOptions.Stateless = true`. Serves protocol 2026-07-28 natively.
- `ServerOptions.SupportedProtocolVersions` includes `2025-06-18` and `2025-11-25`, else legacy clients that open with `initialize` are rejected. An integration test covers a legacy `initialize` + `tools/call` round trip for both versions.
- Bearer check via `auth.RequireBearerToken` with our own `TokenVerifier` backed by fosite introspection. On failure: 401 with `WWW-Authenticate: Bearer resource_metadata="<base>/.well-known/oauth-protected-resource"`.

## 6. OAuth toward MCP clients

The scope is `trakt`; the upstream step is the Trakt authorization-code exchange.

### 6.1 Metadata (RFC 8414)

- `issuer` = `--base-url`; `authorization_endpoint`, `token_endpoint`, `revocation_endpoint`, `registration_endpoint` (only with DCR).
- `response_types_supported: ["code"]`, `grant_types_supported: ["authorization_code","refresh_token"]`.
- `code_challenge_methods_supported: ["S256"]`.
- `token_endpoint_auth_methods_supported: ["none"]`. Claude uses CIMD only when `"none"` is listed.
- `client_id_metadata_document_supported: true`, `authorization_response_iss_parameter_supported: true`.
- `scopes_supported: ["trakt"]`. Trakt itself has no granular scopes; our single scope means "act on my Trakt account".

### 6.2 Clients

- **CIMD (primary).** When `client_id` is an `https://` URL, `GetClient` fetches the JSON document: HTTPS only, public IPs only (SSRF guard on resolved addresses, no redirects to private ranges), 5 s timeout, 64 KiB cap. The document's `client_id` must equal the URL. Cached in SQLite honoring `Cache-Control` (max 24 h). `redirect_uris` match exactly; loopback redirects match ignoring the port. Accepted redirect URIs: `https`, `http` on a loopback host, reverse-domain private-use schemes with a path; userinfo and fragments refused. A failed refetch serves the cached copy for at most 7 days past expiry; a definitive 404, 410 or 400 deletes the cached copy and fails the client.
- **Unauthenticated write limits.** DCR and CIMD documents are capped at 10 `redirect_uris` of at most 2 KiB each and a `client_name` of at most 100 characters. Global token buckets (5/s, burst 20 each) bound DCR registrations and cold CIMD fetches (429 `temporarily_unavailable` with `Retry-After`; a 429 page on `/oauth/authorize`). `GET /oauth/authorize` has its own global bucket (20/s, burst 100).
- **DCR (legacy, on by default).** Public clients only. Registered clients unused for 180 days are purged; clients that never obtained a token are purged after 24 hours.
- Claude's discovery, registration and token calls time out at 10 s; every endpoint answers well under that.

### 6.3 Authorization flow

1. `GET /oauth/authorize`: fosite validates client, redirect URI, PKCE S256 (mandatory), scope `trakt`, and `resource`, which must equal `<base-url>/mcp` (RFC 8707). The pending request is stored in SQLite under a random id, 10-minute TTL.
2. Consent page: shows the client name and, in bold, the redirect URI host ("Claude wants to access your Trakt account. You will be sent back to **claude.ai**."). CSRF token bound to a short-lived cookie. "Deny" returns `access_denied` to the client.
3. `POST /oauth/consent`: redirect to `https://trakt.tv/oauth/authorize` with `response_type=code`, `client_id`, `redirect_uri=<base-url>/oauth/trakt/callback`, `state` = pending request id, PKCE S256 toward Trakt.
4. `GET /oauth/trakt/callback`: exchange the code at `https://api.trakt.tv/oauth/token`, then call `GET /users/settings` to read the Trakt user (slug, username, ids, VIP flag). If Trakt returns `access_denied`, show an error page with a retry link. Upsert the user (Trakt user id, slug), store the encrypted Trakt tokens, call fosite `NewAuthorizeResponse`, redirect to the client with `code`, `state` and `iss` (RFC 9207, added by us).
5. `POST /oauth/token`: fosite handles the code and refresh grants.

### 6.4 Our tokens

- Opaque HMAC tokens (fosite default strategy). SQLite stores signatures only.
- Access token: 1 hour. Authorization code: 10 minutes. Refresh token: rotated on each use, reuse revokes the whole family, expires after 180 days without use.
- The fosite HMAC global secret is derived from the active encryption key with HKDF (label `fosite-hmac`). (Carried over. Consequence: removing an old key after `rekey` invalidates tokens signed under it, so keep old keys listed for 180 days or accept forced reconnects.)

## 7. Trakt side

- Confidential Web OAuth application. Redirect URI `<base-url>/oauth/trakt/callback`. Authorization endpoint `https://trakt.tv/oauth/authorize`; token and revoke endpoints on `https://api.trakt.tv/oauth/`.
- Every API call sends `trakt-api-key: <client id>`, `trakt-api-version: 2`, `Content-Type: application/json`, `Authorization: Bearer <access token>` and a `User-Agent` naming the project and version (no personal data).
- Trakt access and refresh tokens are encrypted with AES-256-GCM, additional data = our user id. `ENCRYPTION_KEYS` is a comma-separated `keyid:base64(32 bytes)` list; the first is active for new writes. `rekey` re-encrypts every row. Rotation: add the new key, restart, run `rekey`, remove the old key.
- **Single-use refresh.** The per-user token source serializes refresh through a per-user lock held across read, call and write. The new token pair is written to SQLite before the lock is released. A failed refresh never discards the stored refresh token, unless Trakt answers `invalid_grant`. Concurrent tool calls share one refresh.- A Trakt 401 drops the cached access token and forces one refresh; if that works the call is retried once, and a second 401 returns "Trakt access was revoked; reconnect this connector". `invalid_grant` on refresh means the user revoked the application at Trakt: delete all rows for that user (which also kills our tokens) and return the same tool error. The client's next MCP request then gets 401 `invalid_token` and restarts the OAuth flow.
- One Trakt grant per user, shared by all of that user's MCP clients. Each new consent replaces the stored Trakt tokens.
- `POST /oauth/revoke` from a client revokes only our tokens for that client, never at Trakt. Only `disconnect` (CLI) and the 180-day purge revoke at Trakt (`POST /oauth/revoke`).
- Per-user write lock: Trakt mutations of one user run sequentially.

## 8. Tools

All tools act on the authenticated user's own account for personal data, and on Trakt's public catalog otherwise. Results carry structured content (`structuredContent`); `content` carries one text block with the serialized JSON of the same output (the 2025-06-18 backwards-compatibility rule, since some clients pass only `content` to the model).

Annotations: every tool has a `title` and explicit `readOnlyHint`, `destructiveHint`, `openWorldHint` (Claude's and OpenAI's directories reject tools without them; `TestToolAnnotations` enforces it). Read tools: `readOnlyHint: true`. Removal and clearing tools (`remove_*`, `cancel_checkin`, `remove_playback_item`) are `destructiveHint: true`. Other write tools: `destructiveHint: false`. Every tool is `openWorldHint: true`. Write tools say in their description that they change the user's real Trakt account.

Identifiers: wherever a tool takes a show, movie or person id, it accepts the Trakt slug (`breaking-bad`), the numeric Trakt id, or an IMDb id (`tt0903747`, `nm0186505` for people). Values are validated against `^[a-z0-9-]+$`, `^[0-9]+$` and `^(tt|nm)[0-9]+$` before Trakt is called, so no path or query injection reaches the API. List tools take `limit` (default 10, max 100) and `page`; without `page` they auto-paginate up to `limit`, with `page` they return one page plus `{page, page_count, item_count}` taken from the `X-Pagination-*` headers.

The registry holds 90 tools. Tools that cover several kinds of item are merged and take a `type` (`movie`, `show`, `season` or `episode`, as each tool lists), with `season` and `episode` when the type needs them. Names follow `get_` (one entity), `list_` (a collection), `search` and the write verbs (`add_`, `remove_`, `update_`, `create_`, `delete_`, `reorder_`).

### 8.1 Catalog: charts and details (24)

Charts (`type` movie or show): `list_trending`, `list_popular`, `list_anticipated`, `list_favorited` (`period`), `list_played` (`period`), `list_watched` (`period`), `list_related`, plus `list_boxoffice`.

Details (`type` movie, show, season or episode): `get_summary` (`extended`), `get_ratings`, `get_stats`, `list_watching`, `list_people` (`include_guest_stars`), `list_videos` (`embed_markdown`), `list_translations` (`language`), `list_item_lists` (`list_type`, `sort`; `list_type` values follow Trakt's enum, singular `watchlist`). Movie or show only: `list_aliases`, `list_studios`. Shows and seasons (these take a show `id` and no `type`): `list_seasons`, `list_season_episodes`, `get_next_episode`, `get_last_episode`, `list_show_certifications`. Movies: `list_movie_releases`.

### 8.2 Search and reference data (7)

`search` (`type` movie or show, `exact`), `search_by_id`, `list_genres`, `list_countries`, `list_languages`, `list_certifications`, `list_networks`.

### 8.3 People (2) and comments (3)

People: `get_person`, `list_person_credits` (`type` movie or show). A person-lists tool is not offered: Trakt's OpenAPI documents `GET /people/{id}/lists/...` as always returning an empty array.

Comments: `list_comments` (`type` movie, show, season or episode), `get_comment`, `list_comment_replies`. Comment tools take `sort` (`likes`, `replies`, `newest`, `oldest`) and `show_spoilers` (default false: spoiler comments are dropped). Reviews are flagged. Comment text is untrusted third-party content: tool descriptions say so, and the output wraps it in a `comment` field the model must not treat as instructions.

### 8.4 The user's account (21)

| Area | Tools |
| --- | --- |
| Watched | `list_user_watched` (`type` movies or shows: last watched, play count), `list_history` (`history_type`, `item_id`, `start_at`, `end_at`), `add_to_history`, `remove_from_history`, `get_show_progress` (`hidden`, `specials`, `count_specials`, `last_activity`, `verbose`), `get_show_collection_progress`, `list_playback_progress` (`playback_type`), `remove_playback_item` |
| Watchlist | `list_watchlist` (`type`: all, movies, shows, seasons, episodes; `sort`: rank, added, title, released, runtime, popularity, percentage, votes), `add_to_watchlist` (optional `notes`, VIP only, 500 characters), `remove_from_watchlist` |
| Collection | `list_collection` (`type`), `add_to_collection`, `remove_from_collection` |
| Ratings | `list_user_ratings`, `add_ratings`, `remove_ratings` |
| Smart lists | `list_smart_lists`, `list_smart_list_items` (`type`, `sort_by`, `sort_how`) |
| Check-in | `checkin` (`type` movie, or show by `id` or `show_title` + `show_year`; `season`, `episode`, `message`), `cancel_checkin` |

Count check: the lists in this section cover the catalog, account and recommendation tools; the registry total is 90 (`TestRegistryHas90Tools`). If a list and a number ever disagree, the lists in this section win and the number is corrected.

Write tools (`add_to_history`, `remove_from_history`, `add_to_watchlist`, `remove_from_watchlist`, `add_to_collection`, `remove_from_collection`, `add_ratings`, `remove_ratings`) take typed items `{type, id}` with `type` one of `movie`, `show`, `season`, `episode`. `id` accepts a Trakt numeric id or an IMDb id; slugs are accepted for `movie` and `show` only (Trakt has no episode slugs). The tool groups items by type into the `movies`, `shows`, `seasons`, `episodes` arrays Trakt expects. A show item marks the whole show; a season or episode item marks only that one. Resolving "the last episode I watched" is the model's job through `get_show_progress` and `list_seasons`.

The batch write tools take up to 50 items per call, plus an optional `watched_at` (ISO 8601, `unknown`, or `released` for episodes), `collected_at` or `rating` (1 to 10). The tool sends one Trakt request per call, never silently splitting a large batch.

`list_smart_lists` lists a user's smart list definitions (default user `me`) through `GET /users/{id}/smart-lists`; `list_smart_list_items` resolves one list through `GET /smart-lists/{list_id}/items/{type}/{sort_by}/{sort_how}` (`type` all, movies or shows; `sort_by` rank, random, title, released, runtime, percentage, votes, imdb_rating, imdb_votes, tmdb_rating, tmdb_votes, rt_tomatometer, rt_audience, metascore, added; `sort_how` asc or desc; `limit`, `page`). Smart list items never go through the normal list endpoints. Finding: smart lists exist in the official API via `/users/{id}/smart-lists` and `/smart-lists/...`, verified from the OpenAPI spec; live behavior (`list_id` shape, private lists with our token) is unverified. Both endpoints are OAuth optional. Query filters would narrow a list on top of its own filters, and `watchnow` filters resolve against the list owner; the tool exposes none of them. `list_id` accepts the same bounded slug or numeric grammar as other ids.

`cancel_checkin` and the movie type of `checkin` are additions to the supplied list: a check-in must be cancellable, and movies are in scope.

### 8.5 Recommendations (3)

`list_recommendations` (`type` movie or show; `ignore_collected`, `ignore_watchlisted`), `hide_recommendation`, `unhide_recommendation`.

### 8.6 Errors returned to the assistant

- Trakt 429, or our own rate limit: tool error "rate limited, retry after N s" using `Retry-After`.
- Trakt 404: "not found". 409 on a duplicate check-in: the active check-in and its expiry are returned.
- Account limit errors (list, watchlist, collection caps) and VIP-only features are mapped by HTTP status only (420 account limit, 423 locked, 426 VIP required; the builder confirms the numbers against Trakt's status-codes page), and Trakt's own message is passed through as "Trakt account limit reached" or "Trakt VIP is required for this feature", never retried.
- Locked account: "this Trakt account is locked".
- Trakt 5xx: tool error with Trakt's status; one automatic retry for idempotent reads only.
- Trakt unreachable (timeout, refused connection, DNS failure): "Trakt is unreachable, retry later", logged at warn with the network cause and never the request URL. A call the client canceled is logged at info with status `canceled`.
- Upstream token dead: section 7.

## 9. Rate limiting

In-memory token bucket per Trakt account, separate buckets for reads and writes, plus one global bucket for the application's own Trakt budget. Flags `--rate-limit-reads` and `--rate-limit-writes` (requests per minute per user). Defaults, my call: 120 reads/min and 30 writes/min per user. Trakt's documented limits (authenticated GET about 1000 per 5 minutes per user, one write per second per user) are checked against the live documentation by the builder and the defaults adjusted. Token buckets set burst to the documented per-window count, never `rate/6`. The application-wide bucket is the flag `--rate-limit-global` (`RATE_LIMIT_GLOBAL`, default 1200 requests per minute, builder's call); a spent budget returns the same "rate limited" tool error. Known mismatch, accepted: the write bucket lets a user burst 30 writes, while Trakt documents about 1 write per second per user, so a batch of writes (tidying a collection) can draw Trakt 429s. Those map to the "rate limited" tool error; the server does not pace writes to 1 per second. The site's terms say so.

## 10. Storage

SQLite, rollback-journal mode. Stored:

- users: Trakt user id, Trakt slug, created, last seen.
- encrypted Trakt access and refresh tokens, access token expiry, key id.
- fosite rows: authorization codes, PKCE, access and refresh token signatures, with expiry.
- clients: DCR registrations, CIMD cache.
- pending authorizations (10-minute TTL).

Never stored: watch history, watchlist, ratings, collection, comments, tool inputs and outputs, any token in clear.

Activity is batched in memory and flushed to `users.last_seen_at` every minute and on store close. Background jobs: hourly deletion of expired fosite rows and pending authorizations; daily purge of users inactive for 180 days (rows deleted, Trakt token revoked at Trakt after the delete succeeded), unused clients (180 days) and clients that never obtained a token (24 hours). The user delete re-checks `last_seen_at` and the stored refresh token in one transaction. Every disconnection logs `user disconnected` at info with `user_id` and `reason` (`inactive`, `trakt_revoked`, `rotated_token_lost`, `cli`). `rotated_token_lost` means Trakt refused a refresh token this process had spent without saving its replacement; `trakt_revoked` covers every other refusal, since Trakt answers `invalid_grant` for both. Shutdown waits for running token refreshes (up to the refresh timeout) before closing the store.

User deletion runs the explicit `deleteUserChildren` list; every new per-user table is added to it. Schema migrations are embedded, numbered sequentially, and start at `001`.

## 11. Public instance, site and privacy

- Operator: Samuel Berthe, personally. The server renders no page: `GET /` redirects to `--site-url`. The Astro site in `site/` serves the landing page, privacy policy and terms.
- The privacy page states what section 10 stores and never stores, how to erase everything (disconnect in the client, revoke the application at Trakt under Settings > Connected Apps, 180-day purge). It says plainly that after a revoke at Trakt, our rows stay until the user's next call fails with `invalid_grant` or the 180-day purge runs, so deletion can take up to 180 days. Only the `disconnect` CLI and the purge delete at once, and that Trakt is the data source and processor of the user's viewing data.
- The site and README never claim endorsement by Trakt. The name "Trakt" is used descriptively; the logo is not used without permission (section 17).

## 12. Logging and metrics

- `log/slog`, JSON to stdout. Never log tokens, codes, secrets or Trakt response bodies.
- `info`: one line per tool call: tool name, our internal user id, duration, status, Trakt HTTP status, result count. No arguments, no results.
- `debug`: also full tool arguments and results, with a startup warning that personal data will be written to logs.
- OAuth events at `info`: client registered, user connected, user disconnected, refresh reuse detected.
- Metrics (with `--metrics`): tool calls by tool and status, latency histograms, Trakt API calls by endpoint group and status, rate-limit rejections, OAuth grants by type. No user or client identifiers in labels. Startup fails if `--metrics` is set without a user and a 16+ character password.

## 13. CLI

Environment variables carry **no prefix**: each flag reads the env var of the same name, uppercased, dashes replaced by underscores (`--db-path` and `DB_PATH`). Flag wins over env. Secrets are environment-only so they never show in process listings. Trakt-specific variables keep `TRAKT_` because it is part of the name.

| Flag / env var | Default | Purpose |
| --- | --- | --- |
| `--base-url` / `BASE_URL` | required | Public HTTPS URL; also the OAuth `issuer` |
| `--site-url` / `SITE_URL` | required for `serve` | Where `GET /` redirects (the Astro site) |
| `--listen` / `LISTEN` | `127.0.0.1:8080` (`0.0.0.0:$PORT` when `PORT` is set) | Listen address |
| `--db-path` / `DB_PATH` | `./data/trakt-mcp.db` | SQLite file |
| `--trakt-client-id` / `TRAKT_CLIENT_ID` | required | Trakt application client id (also sent as `trakt-api-key`) |
| `TRAKT_CLIENT_SECRET` (env only) | optional | Trakt application client secret; deprecated by Trakt, omitted from requests when unset |
| `ENCRYPTION_KEYS` (env only) | required | `keyid:base64` list, first active |
| `--enable-dcr` / `ENABLE_DCR` | `true` | Keep Dynamic Client Registration |
| `--log-level` / `LOG_LEVEL` | `info` | `debug`, `info`, `warn`, `error` |
| `--metrics` / `METRICS` | `false` | Enable Prometheus endpoint |
| `--metrics-path` / `METRICS_PATH` | `/metrics` | Endpoint path |
| `--metrics-listen` / `METRICS_LISTEN` | main listener | Optional separate address |
| `--metrics-user` / `METRICS_USER` | none | Basic auth user, required with `--metrics` |
| `--metrics-password` / `METRICS_PASSWORD` | none | 16+ characters, required unless the file flag is set |
| `--metrics-password-file` / `METRICS_PASSWORD_FILE` | none | Safer alternative to the CLI password |
| `--rate-limit-reads` / `RATE_LIMIT_READS` | `120` | Per-user reads per minute |
| `--rate-limit-writes` / `RATE_LIMIT_WRITES` | `30` | Per-user writes per minute |
| `--rate-limit-global` / `RATE_LIMIT_GLOBAL` | `1200` | Trakt requests per minute across all users |
| `--openai-apps-challenge` / `OPENAI_APPS_CHALLENGE` | none | Serves `/.well-known/openai-apps-challenge` |

Subcommands: `serve` (default), `genkey`, `rekey`, `disconnect <trakt-slug-or-id>` (revokes at Trakt, deletes all rows), `version`.

The unprefixed names (`BASE_URL`, `LISTEN`, `LOG_LEVEL`, `METRICS`) can collide with variables set by a host or shell. `docs/self-hosting.md` states the full list so operators can check.

## 14. Deliverables

- Binary releases (linux, darwin; amd64, arm64) as in `release.yml`. No Docker image is published today.
- ChatGPT/Codex plugin package in `plugin/openai/` (tool names, titles and descriptions are regenerated from the new tool set; bump `version` in `plugin.json` first).
- Astro site in `site/`: landing page, privacy policy, terms of service, rewritten for Trakt.
- README with tutorials: what is stored and how to erase it; Trakt application setup (create an app at trakt.tv/oauth/applications, redirect URI `https://<host>/oauth/trakt/callback`, secret rotation, encryption key rotation); deploy; connect Claude and ChatGPT; example prompts ("add the last episode I watched of X", "what is in my watchlist that I can watch tonight", "tidy my collection").
- `docs/self-hosting.md`, `docs/security.md`, `docs/directory-submission.md` updated to Trakt.
- GitHub issues labelled `enhancement` for the non-goals of section 2.

## 15. Testing

- Fake Trakt API on `httptest`, with one fixture per used endpoint, hand-written from the OpenAPI examples and docs. A script `scripts/record-fixtures` (run by the user against a real Trakt account, never in CI) refreshes them and strips personal data. The fake covers success, 401, 404, 409, 420-style account limits, 423 locked account, 429, 5xx, pagination headers, malformed JSON, slow responses.
- Refresh tests: two concurrent calls with an expired token trigger exactly one refresh; a refresh that fails with a network error keeps the old refresh token; `invalid_grant` deletes the user.
- Full OAuth flow test with a fake Trakt (authorize, consent, callback, token, refresh rotation, reuse revocation, PKCE failure, wrong `resource`, denied consent).
- CIMD fetcher tests: private IP, redirect to private IP, oversize, timeout, mismatched `client_id`.
- MCP legacy `initialize` round trip for 2025-06-18 and 2025-11-25, plus a native 2026-07-28 call.
- Tool tests: id validation table (slug, numeric, IMDb, injection attempts, empty), batch size 0, 1, 50, 51, `watched_at` variants, comment spoiler filter, annotation test over every registered tool.
- Codegen test: `make generate` produces no diff.
- No real Trakt account in CI.

## 16. Left to the builder

- Package layout inside `internal/trakt` and `internal/tools`, file names, SQLite schema details.
- Exact JSON shapes of tool inputs and outputs, within section 8.
- Site wording (privacy text must match section 10).
- Which Trakt spec shapes need patches in the filtered spec.

## 17. Open items

- Read Trakt's API use policy (`https://developer.trakt.tv/docs/api-use-policy`) and confirm that a public hosted multi-tenant app driven by an LLM is allowed, whether an app review is needed, and what the branding rules are. Blocks the public launch, not the build.
- Verify Trakt's current rate limits and the exact account-limit status codes against the live documentation.
- Smart lists: they exist in the official API via `/users/{id}/smart-lists` and `/smart-lists/...`, verified from the OpenAPI spec (both tools shipped, count 79). Live behavior is unverified: the shape of `list_id` (slug or numeric), and whether our token can read a private smart list.
- Smart-list `list_id` grammar: the tool accepts a bounded slug or a numeric id, so a real smart-list slug containing `_` or `.` may be rejected before any Trakt call. Unverified live.
- Confirm the public hostname.
- Test whether ChatGPT scheduled tasks can call developer-mode apps.
