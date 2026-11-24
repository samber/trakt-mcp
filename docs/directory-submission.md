# Directory submissions

How the hosted instance, `https://mcp.trakt-mcp.samber.dev/mcp`, gets listed in two directories:

- Claude: Connectors Directory, submitted at [claude.ai/directory/manage](https://claude.ai/directory/manage) as an **MCP connector**.
- ChatGPT and Codex: Plugins Directory, submitted at [platform.openai.com/plugins](https://platform.openai.com/plugins) as a ZIP package.

Both submissions happen in the vendors' portals. This repository holds what the portals ask for.

## What the repository already provides

| Requirement                                        | Where                                                                     |
| -------------------------------------------------- | ------------------------------------------------------------------------- |
| Remote HTTPS Streamable HTTP endpoint              | `POST /mcp`                                                               |
| OAuth 2.1 with PKCE, DCR and CIMD                  | `internal/oauth`                                                          |
| `title` on every tool                              | `internal/tools/registry.go`                                              |
| `readOnlyHint`, `destructiveHint`, `openWorldHint` | `internal/tools/registry.go`, pinned by `TestToolAnnotations`             |
| Privacy policy, terms, support contact             | `site/src/pages/privacy.astro`, `terms.astro`, `OPERATOR_EMAIL`           |
| OpenAI domain verification                         | `--openai-apps-challenge`, served at `/.well-known/openai-apps-challenge` |
| OpenAI plugin package                              | `plugin/openai/`, built by `make plugin-openai`                           |

## Blockers before either review can pass

1. **Trakt API use policy.** Confirm that a public, multi-tenant, LLM-driven app is allowed, whether Trakt reviews apps, and its branding rules (spec section 17).
2. **Reviewer account.** Both vendors need a Trakt account that holds sample data (watchlist, history, ratings) and needs no MFA prompt, SMS code or private network. Create a dedicated one. Enter its credentials only in the portals, never in this repository or the ZIP.
3. **Demo video.** OpenAI asks for a recording URL that shows the test cases below.
4. **Identity.** OpenAI requires a verified individual or business identity under the exact name it publishes. The package says `Samuel Berthe`.
5. **Public hostname.** `trakt-mcp.samber.dev` is a placeholder until confirmed. The OpenAI MCP URL cannot change after submission.

## Claude connector

Portal steps and the answers this project gives:

| Step       | Answer                                                                                      |
| ---------- | ------------------------------------------------------------------------------------------- |
| Connection | `https://mcp.trakt-mcp.samber.dev/mcp`, one URL for every user.                             |
| Tools      | Synced from the server. Every tool carries a title and hints.                               |
| Listing    | Text below. Documentation URL: `https://trakt-mcp.samber.dev`. Privacy: `/privacy`.         |
| Use cases  | Reads and writes. Users need a Trakt account; some features need Trakt VIP.                 |
| Company    | Samuel Berthe, `https://github.com/samber`, contact `hello@samuel-berthe.fr`.               |
| Auth       | Required. OAuth 2.0, server speaks CIMD and DCR, so no client id is entered in the portal.  |
| Data       | First-party: the server calls the Trakt API for the signed-in user. No health data. No ads. |
| Test       | Run the tools in MCP Inspector and as a custom connector in Claude, then tick the box.      |

Listing text (limits: name 100, one-liner 200, description 2,000 characters):

- Name: `Trakt MCP`
- One-liner: `Browse movies and shows, and manage your Trakt watchlist, history, collection and ratings from Claude.`
- Description: `Ask Claude what is trending, find a show or movie, check cast and ratings, see which episode comes next, add titles to your watchlist, mark episodes as watched, rate what you saw, or check in to what you are watching now. Sign in with your Trakt account. Your viewing data is read and written live through the Trakt API and is never stored on the server. Tools that change your Trakt account say so, and removal tools are flagged as destructive so Claude asks first. Independent open source project, not affiliated with or endorsed by Trakt.`

Claude's OAuth callback is `https://claude.ai/api/mcp/auth_callback`. The server accepts any `https` redirect URI from a registered or CIMD client, so nothing to allow-list.

## OpenAI plugin

1. Deploy with `OPENAI_APPS_CHALLENGE` unset. In the portal, create the plugin and copy its domain-verification token.
2. Set `OPENAI_APPS_CHALLENGE=<token>`, redeploy, and check the body is exactly the token:

   ```sh
   curl -s https://mcp.trakt-mcp.samber.dev/.well-known/openai-apps-challenge
   ```

3. Bump `version` in `plugin/openai/plugin.json`, then build the package and upload it:

   ```sh
   make plugin-openai   # writes dist/trakt-openai-plugin.zip
   ```

4. Fix validation findings, fill the review details, submit.

Both listings are named `Trakt MCP` and say the project is independent. Either review may still flag the name as implying affiliation with Trakt; rename it in `plugin.json` (bump `version`) and in the Claude portal if so. Never use the Trakt logo.

Package rules worth remembering: one remote MCP server, no `headers`, no credentials in the ZIP, no `.app.json`. The MCP URL cannot change through the update flow, so the host stays `mcp.trakt-mcp.samber.dev`. A change to the package needs a new `version` in `plugin/openai/plugin.json`.

`publication.countries` is `[]`, which OpenAI documents as "no country restriction". Omitting the field does not mean the same: it keeps existing targeting. List uppercase country codes in `plugin.json` to restrict availability. Projects with EU data residency cannot submit MCP plugins today.

### Test cases

Five that should work:

| Prompt                                                | Tools                                 | Expected behavior                               |
| ----------------------------------------------------- | ------------------------------------- | ----------------------------------------------- |
| What are the trending shows this week?                | `fetch_trending_shows`                | Returns a short list of trending shows.         |
| Add Dune: Part Two to my watchlist.                   | `search_movies`, `add_to_watchlist`   | Finds the movie, then adds it to the watchlist. |
| What is the next episode of Severance I should watch? | `search_shows`, `fetch_show_progress` | Returns the next unwatched episode.             |
| Mark Severance season 1 episode 1 as watched.         | `search_shows`, `add_to_history`      | Adds one play to the user's history.            |
| Rate Arrival 9 out of 10.                             | `search_movies`, `add_ratings`        | Saves the rating.                               |

Three that should not trigger the plugin:

| Prompt                                 | Why                              |
| -------------------------------------- | -------------------------------- |
| Play the next episode of Severance.    | The plugin cannot play media.    |
| Buy two tickets for Dune tonight.      | No ticketing or payment.         |
| Write a review of Arrival and post it. | Comment writing is out of scope. |
