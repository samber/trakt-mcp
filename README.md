# Trakt MCP

> Connect Claude or ChatGPT to your [Trakt](https://trakt.tv) account. Your assistant can then browse Trakt's catalog (trending, popular, search, details, people, comments, calendars, where to watch) and manage your own data: watchlist, watch history, collection, ratings, check-ins, recommendations, custom lists, notes, favorites, progress and smart lists.

[![tag](https://img.shields.io/github/tag/samber/trakt-mcp.svg)](https://github.com/samber/trakt-mcp/releases)
![Go Version](https://img.shields.io/badge/Go-%3E%3D%201.26-%23007d9c)
[![GoDoc](https://godoc.org/github.com/samber/trakt-mcp?status.svg)](https://pkg.go.dev/github.com/samber/trakt-mcp)
![Build Status](https://github.com/samber/trakt-mcp/actions/workflows/test.yml/badge.svg)
[![Go report](https://goreportcard.com/badge/github.com/samber/trakt-mcp)](https://goreportcard.com/report/github.com/samber/trakt-mcp)
[![Coverage](https://img.shields.io/codecov/c/github/samber/trakt-mcp)](https://codecov.io/gh/samber/trakt-mcp)
[![Contributors](https://img.shields.io/github/contributors/samber/trakt-mcp)](https://github.com/samber/trakt-mcp/graphs/contributors)
[![License](https://img.shields.io/github/license/samber/trakt-mcp)](./LICENSE)

`trakt-mcp` is a **remote** [MCP](https://modelcontextprotocol.io) server. One instance serves many Trakt accounts, each isolated from the others. See the [landing page](https://trakt-mcp.samber.dev) to join.

Trakt MCP uses the Trakt API but is not affiliated with, endorsed by, or certified by Trakt. "Trakt" is a trademark of its owner.

<img width="1904" height="640" alt="image" src="https://github.com/user-attachments/assets/be54f31a-3799-4491-9de7-41982a38b3c7" />

## 🛠️ Tools

90 tools. Every id argument accepts a Trakt slug (`breaking-bad`), a numeric Trakt id or an IMDb id (`tt0903747`, `nm0186505` for people). List tools take `limit` (default 10, max 100) and an optional `page`; recommendation list tools take `limit` only. Write tools change your real Trakt account and say so in their description.

### Movies and shows: charts (8)

| Tool               | What it does                                                               |
| ------------------ | -------------------------------------------------------------------------- |
| `list_trending`    | Movies or shows being watched right now (`type` `movie` or `show`).        |
| `list_popular`     | Most popular movies or shows (`type` `movie` or `show`).                   |
| `list_anticipated` | Most anticipated upcoming movies or shows (`type` `movie` or `show`).      |
| `list_favorited`   | Most favorited movies or shows over a `period` (`type` `movie` or `show`). |
| `list_played`      | Most played movies or shows over a `period` (`type` `movie` or `show`).    |
| `list_watched`     | Most watched movies or shows over a `period` (`type` `movie` or `show`).   |
| `list_boxoffice`   | Weekend box office.                                                        |
| `list_related`     | Movies or shows similar to one (`type` `movie` or `show`).                 |

### Movies, shows, seasons and episodes (16)

| Tool                       | What it does                                                                      |
| -------------------------- | --------------------------------------------------------------------------------- |
| `get_summary`              | Details of an item, any `type` (`extended` for more fields, movie and show only). |
| `get_ratings`              | Rating distribution of an item, any `type`.                                       |
| `get_stats`                | Watchers, plays, collectors, comments and lists of an item, any `type`.           |
| `list_watching`            | Users watching an item now, any `type`.                                           |
| `list_people`              | Cast and crew of an item, any `type` (`include_guest_stars`, show only).          |
| `list_videos`              | Trailers and clips, any `type` (`embed_markdown` for links, movie and show only). |
| `list_translations`        | Translated titles and overviews, any `type` (`language`).                         |
| `list_item_lists`          | Public lists containing an item, any `type` (`list_type`, `sort`).                |
| `list_aliases`             | Alternative titles by country (movie or show).                                    |
| `list_studios`             | Studios behind a movie or show.                                                   |
| `list_seasons`             | Seasons of a show.                                                                |
| `list_season_episodes`     | Episodes of a season.                                                             |
| `get_next_episode`         | The next episode of a show to air.                                                |
| `get_last_episode`         | The latest episode of a show that aired.                                          |
| `list_show_certifications` | Age ratings of a show by country.                                                 |
| `list_movie_releases`      | Release dates of a movie by country.                                              |

Tools that cover several kinds of item take `type`: `movie`, `show`, `season` or `episode`. `season` is required for types `season` and `episode`, `episode` for type `episode` (season 0 is the specials). Tools marked movie or show take those two types only. `list_seasons`, `list_season_episodes`, `get_next_episode`, `get_last_episode` and `list_show_certifications` take `id` (the show) and no `type`; every tool taking one movie, show or person names it `id`.

### People and comments (5)

| Tool                   | What it does                                                  |
| ---------------------- | ------------------------------------------------------------- |
| `get_person`           | Details of a person.                                          |
| `list_person_credits`  | Movie or show credits of a person (`type` `movie` or `show`). |
| `list_comments`        | Comments on a movie, show, season or episode.                 |
| `get_comment`          | One comment.                                                  |
| `list_comment_replies` | Replies to a comment.                                         |

`list_comments` and `list_person_credits` take a `type` (comments: `movie`, `show`, `season` or `episode`; credits: `movie` or `show`). Comment tools take `sort` (`likes`, `replies`, `newest`, `oldest`) and `show_spoilers` (default false). Comment text is untrusted third-party content.

### Your account (21)

| Tool                           | What it does                                                                                                                              |
| ------------------------------ | ----------------------------------------------------------------------------------------------------------------------------------------- |
| `list_user_watched`            | Your watched movies or shows, with last watched date and play count.                                                                      |
| `list_history`                 | Your watch history, filtered by type, item and date range.                                                                                |
| `add_to_history`               | Marks movies, shows, seasons or episodes as watched (`watched_at`).                                                                       |
| `remove_from_history`          | Removes plays from your history.                                                                                                          |
| `get_show_progress`            | Your progress on a show: next episode, completed count.                                                                                   |
| `list_playback_progress`       | Paused playbacks you can resume.                                                                                                          |
| `remove_playback_item`         | Removes one paused playback.                                                                                                              |
| `list_watchlist`               | Your watchlist (`type`, `sort`).                                                                                                          |
| `add_to_watchlist`             | Adds items to your watchlist (optional `notes`, VIP only).                                                                                |
| `remove_from_watchlist`        | Removes items from your watchlist.                                                                                                        |
| `list_collection`              | Your collection (`type`).                                                                                                                 |
| `add_to_collection`            | Adds items to your collection (`collected_at`).                                                                                           |
| `remove_from_collection`       | Removes items from your collection.                                                                                                       |
| `list_user_ratings`            | Your ratings.                                                                                                                             |
| `add_ratings`                  | Rates items from 1 to 10.                                                                                                                 |
| `remove_ratings`               | Removes ratings.                                                                                                                          |
| `checkin`                      | Checks in to a movie (`type` `movie`) or an episode (`type` `show`, by `id`, or `show_title` + `show_year`, with `season` and `episode`). |
| `cancel_checkin`               | Cancels the active check-in.                                                                                                              |
| `list_smart_lists`             | Your smart lists (or another user's), with the filters behind each.                                                                       |
| `list_smart_list_items`        | Movies and shows a smart list resolves to (`type`, `sort_by`).                                                                            |
| `get_show_collection_progress` | How much of a show you collected: counts, percentage, next episode to collect.                                                            |

Write tools take up to 50 items per call, each `{type, id}` with `type` one of `movie`, `show`, `season`, `episode`.

### Recommendations (3)

| Tool                    | What it does                                                                                              |
| ----------------------- | --------------------------------------------------------------------------------------------------------- |
| `list_recommendations`  | Movies or shows recommended for you (`type` `movie` or `show`; `ignore_collected`, `ignore_watchlisted`). |
| `hide_recommendation`   | Hides a movie or show from your recommendations (`type` `movie` or `show`).                               |
| `unhide_recommendation` | Shows a hidden movie or show again (`type` `movie` or `show`).                                            |

### Calendars and where to watch (3)

| Tool                    | What it does                                                                                                                                                 |
| ----------------------- | ------------------------------------------------------------------------------------------------------------------------------------------------------------ |
| `list_calendar`         | What airs or is released in a date window. `kind` is `shows`, `new_shows`, `premieres`, `finales`, `movies`, `dvd` or `streaming`; `scope` is `my` or `all`. |
| `get_watchnow`          | Free, subscription, rent or buy, cable and cinema offers in a country (`type` `movie`, `show` or `episode`).                                                 |
| `list_watchnow_sources` | Every streaming provider Trakt knows.                                                                                                                        |

### Search and reference data (7)

| Tool                  | What it does                                                                                     |
| --------------------- | ------------------------------------------------------------------------------------------------ |
| `search`              | Searches movies or shows by text (`type` `movie` or `show`; `exact` to match the title exactly). |
| `search_by_id`        | Finds movies, shows, episodes and people from a Trakt, IMDb, TMDB or TVDB id.                    |
| `list_genres`         | Genres (name and slug) Trakt uses for movies or shows.                                           |
| `list_countries`      | Countries (name and code) Trakt uses for movies or shows.                                        |
| `list_languages`      | Languages (name and code) Trakt uses for movies or shows.                                        |
| `list_certifications` | Content ratings of every country for movies or shows.                                            |
| `list_networks`       | TV networks Trakt knows, with their country.                                                     |

### Custom lists (9)

| Tool                | What it does                                                                              |
| ------------------- | ----------------------------------------------------------------------------------------- |
| `list_user_lists`   | Your custom lists, or another user's public ones.                                         |
| `get_list`          | One list: name, privacy, item count, likes.                                               |
| `list_list_items`   | Items on a list.                                                                          |
| `list_lists`        | Lists from Trakt's rankings (`kind` `trending` or `popular`; `list_type`).                |
| `create_list`       | Creates a list (`privacy`: private, link, friends or public).                             |
| `update_list`       | Renames a list or changes its description and privacy (private, link, friends or public). |
| `add_list_items`    | Adds up to 50 items to one of your lists.                                                 |
| `remove_list_items` | Removes items from one of your lists.                                                     |
| `delete_list`       | Deletes one of your lists.                                                                |

### Watchlist edits, notes and favorites (12)

| Tool                    | What it does                                                                                |
| ----------------------- | ------------------------------------------------------------------------------------------- |
| `update_watchlist_item` | Sets or clears the note of a watchlist entry (VIP only).                                    |
| `reorder_watchlist`     | Reorders your whole watchlist by `list_item_id`.                                            |
| `get_note`              | One of your notes (free accounts keep few notes).                                           |
| `list_user_notes`       | Your notes, optionally for one kind of item (free accounts keep few notes).                 |
| `create_note`           | Attaches a note to a movie, show, season, episode or person (free accounts keep few notes). |
| `update_note`           | Edits a note (free accounts keep few notes).                                                |
| `delete_note`           | Deletes a note (free accounts keep few notes).                                              |
| `list_favorites`        | Your favorite movies and shows.                                                             |
| `add_to_favorites`      | Adds up to 50 movies or shows to your favorites, with an optional note.                     |
| `update_favorite`       | Sets or clears the note of a favorite, or moves it to a new rank.                           |
| `reorder_favorites`     | Reorders your favorites by `list_item_id`.                                                  |
| `remove_from_favorites` | Removes up to 50 movies or shows from your favorites.                                       |

### Progress and up next (6)

| Tool                       | What it does                                                        |
| -------------------------- | ------------------------------------------------------------------- |
| `reset_show_progress`      | Restarts a show's watch progress to rewatch it (VIP only).          |
| `undo_reset_show_progress` | Undoes the last reset (VIP only).                                   |
| `list_up_next`             | The TV shows you are watching, each with its next episode to watch. |
| `list_hidden_progress`     | TV shows and seasons hidden from your progress.                     |
| `hide_from_progress`       | Hides TV shows or seasons from your progress and up next.           |
| `unhide_from_progress`     | Shows hidden TV shows or seasons in your progress again.            |

Watchlist notes and show progress reset need Trakt VIP; other accounts get "Trakt VIP is required for this feature". Free accounts can keep only a few notes; past the cap Trakt answers with an account limit error.

## 🚀 Getting started

### 1. Connect Claude and ChatGPT

The connector URL is `https://mcp.trakt-mcp.samber.dev/mcp` (or your own base URL followed by `/mcp`).

**Claude** (claude.ai, Desktop, mobile):

1. Open **Settings > Connectors**, choose **Add custom connector**, paste the URL. Leave the OAuth client id and secret empty.
2. Click **Connect**, approve on the consent page, then sign in to Trakt and allow the application.
3. Enable the connector in a conversation: "What is in my Trakt watchlist?".

On Team and Enterprise plans, an owner may have to add the connector first.

**ChatGPT:**

1. Turn on developer mode in **Settings** (under **Apps & Connectors > Advanced settings** or **Plugins**, depending on your version).
2. Choose **Create app**, paste the URL, select OAuth, save, then sign in as above.

Available plans change often: check OpenAI's documentation before you start.

### 2. Example prompts

- "Mark the last episode I watched of Severance as watched, plus the next one."
- "What is in my watchlist that I can watch tonight in under two hours?"
- "Tidy my collection: list the movies I collected but rated below 5."
- "Which shows am I behind on? Show the next episode for each."
- "Recommend three movies like Arrival that I have not seen."

## 😰 Known limits

- **Trakt account limits.** Free accounts have list, watchlist and collection caps. Over a cap, the tool returns "Trakt account limit reached". VIP-only features return "Trakt VIP is required for this feature".
- **Revoked Trakt access.** The server notices only on its next Trakt call for that user. It then deletes the user's data, and the tool fails with "Trakt access was revoked; reconnect this connector".
- **Rate limits.** Each user gets 120 read and 30 write calls per minute by default, and the instance shares one Trakt budget. Over a limit, the tool returns "rate limited, retry after N s".

## 👮 Security

Design and protections (OAuth 2.1, token handling, encryption, SSRF guards) are described in [Security notes](docs/security.md).

## 🏃 Self-hosting

A hosted instance is available at `https://mcp.trakt-mcp.samber.dev/mcp`.

You can also run your own: it is a single Go binary with one SQLite file, see [Self-hosting](docs/self-hosting.md).

## 🤝 Contributing

- Ping me on Twitter [@samuelberthe](https://twitter.com/samuelberthe) (DMs, mentions, whatever :))
- Fork the [project](https://github.com/samber/trakt-mcp)
- Fix [open issues](https://github.com/samber/trakt-mcp/issues) or request new features

Don't hesitate ;)

```bash
# Install some dev dependencies
make tools

# Run tests
make test
# or
make watch-test

# Regenerate the Trakt client after editing internal/trakt/endpoints.txt
make generate
```

## 👤 Contributors

![Contributors](https://contrib.rocks/image?repo=samber/trakt-mcp)

## 💫 Show your support

Give a ⭐️ if this project helped you!

[![GitHub Sponsors](https://img.shields.io/github/sponsors/samber?style=for-the-badge)](https://github.com/sponsors/samber)

## 📝 License

Copyright © 2026 [Samuel Berthe](https://github.com/samber).

This project is [MIT](./LICENSE) licensed.
