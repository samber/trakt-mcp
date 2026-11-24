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

143 tools. Every id argument accepts a Trakt slug (`breaking-bad`), a numeric Trakt id or an IMDb id (`tt0903747`, `nm0186505` for people). List tools take `limit` (default 10, max 100) and an optional `page`; recommendation list tools take `limit` only. Write tools change your real Trakt account and say so in their description.

### Shows (13)

| Tool                      | What it does                                     |
| ------------------------- | ------------------------------------------------ |
| `fetch_trending_shows`    | Shows being watched right now.                   |
| `fetch_popular_shows`     | Most popular shows.                              |
| `fetch_anticipated_shows` | Most anticipated upcoming shows.                 |
| `fetch_favorited_shows`   | Most favorited shows over a `period`.            |
| `fetch_played_shows`      | Most played shows over a `period`.               |
| `fetch_watched_shows`     | Most watched shows over a `period`.              |
| `search_shows`            | Searches shows by text.                          |
| `fetch_show_ratings`      | Rating distribution of a show.                   |
| `fetch_show_summary`      | Details of a show (`extended` for more fields).  |
| `fetch_show_videos`       | Trailers and clips (`embed_markdown` for links). |
| `fetch_related_shows`     | Shows similar to a show.                         |
| `fetch_show_seasons`      | Seasons of a show.                               |
| `fetch_show_people`       | Cast and crew (`include_guest_stars`).           |

### Seasons (10)

| Tool                        | What it does                                       |
| --------------------------- | -------------------------------------------------- |
| `fetch_season_info`         | Details of one season.                             |
| `fetch_season_episodes`     | Episodes of a season.                              |
| `fetch_season_ratings`      | Rating distribution of a season.                   |
| `fetch_season_stats`        | Watchers, plays, comments, lists.                  |
| `fetch_season_people`       | Cast and crew of a season.                         |
| `fetch_season_videos`       | Videos of a season.                                |
| `fetch_season_watching`     | Users watching the season now.                     |
| `fetch_season_translations` | Translated titles and overviews (`language`).      |
| `fetch_season_lists`        | Lists containing the season (`list_type`, `sort`). |
| `fetch_season_comments`     | Comments on a season.                              |

### Episodes (9)

| Tool                         | What it does                       |
| ---------------------------- | ---------------------------------- |
| `fetch_episode_summary`      | Details of one episode.            |
| `fetch_episode_ratings`      | Rating distribution of an episode. |
| `fetch_episode_stats`        | Watchers, plays, comments, lists.  |
| `fetch_episode_people`       | Cast and crew of an episode.       |
| `fetch_episode_videos`       | Videos of an episode.              |
| `fetch_episode_watching`     | Users watching the episode now.    |
| `fetch_episode_translations` | Translated titles and overviews.   |
| `fetch_episode_lists`        | Lists containing the episode.      |
| `fetch_episode_comments`     | Comments on an episode.            |

### Movies (14)

| Tool                       | What it does                         |
| -------------------------- | ------------------------------------ |
| `fetch_trending_movies`    | Movies being watched right now.      |
| `fetch_popular_movies`     | Most popular movies.                 |
| `fetch_anticipated_movies` | Most anticipated upcoming movies.    |
| `fetch_favorited_movies`   | Most favorited movies over a period. |
| `fetch_played_movies`      | Most played movies over a period.    |
| `fetch_watched_movies`     | Most watched movies over a period.   |
| `fetch_boxoffice_movies`   | Weekend box office.                  |
| `search_movies`            | Searches movies by text.             |
| `fetch_movie_ratings`      | Rating distribution of a movie.      |
| `fetch_movie_summary`      | Details of a movie.                  |
| `fetch_movie_videos`       | Trailers and clips.                  |
| `fetch_related_movies`     | Movies similar to a movie.           |
| `fetch_movie_people`       | Cast and crew.                       |
| `fetch_movie_comments`     | Comments on a movie.                 |

### People (3) and comments (3)

| Tool                    | What it does               |
| ----------------------- | -------------------------- |
| `fetch_person_summary`  | Details of a person.       |
| `fetch_person_movies`   | Movie credits of a person. |
| `fetch_person_shows`    | Show credits of a person.  |
| `fetch_show_comments`   | Comments on a show.        |
| `fetch_comment`         | One comment.               |
| `fetch_comment_replies` | Replies to a comment.      |

Comment tools take `sort` (`likes`, `replies`, `newest`, `oldest`) and `show_spoilers` (default false). Comment text is untrusted third-party content.

### Your account (21)

| Tool                      | What it does                                                           |
| ------------------------- | ---------------------------------------------------------------------- |
| `fetch_user_watched`      | Your watched movies or shows, with last watched date and play count.   |
| `fetch_history`           | Your watch history, filtered by type, item and date range.             |
| `add_to_history`          | Marks movies, shows, seasons or episodes as watched (`watched_at`).    |
| `remove_from_history`     | Removes plays from your history.                                       |
| `fetch_show_progress`     | Your progress on a show: next episode, completed count.                |
| `fetch_playback_progress` | Paused playbacks you can resume.                                       |
| `remove_playback_item`    | Removes one paused playback.                                           |
| `fetch_watchlist`         | Your watchlist (`type`, `sort`).                                       |
| `add_to_watchlist`        | Adds items to your watchlist (optional `notes`, VIP only).             |
| `remove_from_watchlist`   | Removes items from your watchlist.                                     |
| `fetch_collection`        | Your collection (`type`).                                              |
| `add_to_collection`       | Adds items to your collection (`collected_at`).                        |
| `remove_from_collection`  | Removes items from your collection.                                    |
| `fetch_ratings`           | Your ratings.                                                          |
| `add_ratings`             | Rates items from 1 to 10.                                              |
| `remove_ratings`          | Removes ratings.                                                       |
| `checkin_to_show`         | Checks in to an episode (by `show_id`, or `show_title` + `show_year`). |
| `checkin_to_movie`        | Checks in to a movie.                                                  |
| `cancel_checkin`          | Cancels the active check-in.                                           |
| `fetch_smart_lists`       | Your smart lists (or another user's), with the filters behind each.    |
| `fetch_smart_list_items`  | Movies and shows a smart list resolves to (`type`, `sort_by`).         |

Write tools take up to 50 items per call, each `{type, id}` with `type` one of `movie`, `show`, `season`, `episode`.

### Recommendations (6)

| Tool                          | What it does                                                           |
| ----------------------------- | ---------------------------------------------------------------------- |
| `fetch_movie_recommendations` | Movies recommended for you (`ignore_collected`, `ignore_watchlisted`). |
| `fetch_show_recommendations`  | Shows recommended for you (same filters).                              |
| `hide_movie_recommendation`   | Hides a movie from your recommendations.                               |
| `hide_show_recommendation`    | Hides a show from your recommendations.                                |
| `unhide_movie_recommendation` | Shows a hidden movie again.                                            |
| `unhide_show_recommendation`  | Shows a hidden show again.                                             |

### Calendars (7)

| Tool                            | What it does                                              |
| ------------------------------- | --------------------------------------------------------- |
| `fetch_calendar_shows`          | Episodes airing in a date window (`scope` `my` or `all`). |
| `fetch_calendar_new_shows`      | New shows whose first season starts in the window.        |
| `fetch_calendar_show_premieres` | Season premieres in the window.                           |
| `fetch_calendar_show_finales`   | Season finales in the window.                             |
| `fetch_calendar_movies`         | Movies released in cinemas in the window.                 |
| `fetch_calendar_dvd`            | Movies released on DVD or Blu-ray in the window.          |
| `fetch_calendar_streaming`      | Movies arriving on streaming in the window.               |

### Where to watch (4)

| Tool                     | What it does                                                                       |
| ------------------------ | ---------------------------------------------------------------------------------- |
| `fetch_movie_watchnow`   | Free, subscription, rent or buy, cable and cinema offers for a movie in a country. |
| `fetch_show_watchnow`    | The same for a show.                                                               |
| `fetch_episode_watchnow` | The same for an episode.                                                           |
| `fetch_watchnow_sources` | Every streaming provider Trakt knows.                                              |

### Search by id and reference data (8)

| Tool                   | What it does                                                                  |
| ---------------------- | ----------------------------------------------------------------------------- |
| `search_by_id`         | Finds movies, shows, episodes and people from a Trakt, IMDb, TMDB or TVDB id. |
| `search_movies_exact`  | Searches movies by exact title.                                               |
| `search_shows_exact`   | Searches shows by exact title.                                                |
| `fetch_genres`         | Genres Trakt uses for movies or shows.                                        |
| `fetch_countries`      | Countries Trakt uses.                                                         |
| `fetch_languages`      | Languages Trakt uses.                                                         |
| `fetch_certifications` | Age ratings by country.                                                       |
| `fetch_networks`       | TV networks Trakt knows.                                                      |

### Movie and show extras (16)

| Tool                        | What it does                                                |
| --------------------------- | ----------------------------------------------------------- |
| `fetch_movie_stats`         | Watchers, plays, collectors, comments and lists of a movie. |
| `fetch_movie_watching`      | Users watching the movie now.                               |
| `fetch_movie_translations`  | Translated titles and overviews (`language`).               |
| `fetch_movie_aliases`       | Alternative titles by country.                              |
| `fetch_movie_studios`       | Studios behind the movie.                                   |
| `fetch_movie_lists`         | Lists containing the movie.                                 |
| `fetch_movie_releases`      | Release dates by country.                                   |
| `fetch_show_stats`          | Watchers, plays, collectors, comments and lists of a show.  |
| `fetch_show_watching`       | Users watching the show now.                                |
| `fetch_show_translations`   | Translated titles and overviews (`language`).               |
| `fetch_show_aliases`        | Alternative titles by country.                              |
| `fetch_show_studios`        | Studios behind the show.                                    |
| `fetch_show_lists`          | Lists containing the show.                                  |
| `fetch_show_next_episode`   | The next episode to air.                                    |
| `fetch_show_last_episode`   | The latest episode that aired.                              |
| `fetch_show_certifications` | Age ratings by country.                                     |

### Custom lists (10)

| Tool                   | What it does                                           |
| ---------------------- | ------------------------------------------------------ |
| `fetch_user_lists`     | Your custom lists, or another user's public ones.      |
| `fetch_list`           | One list: name, privacy, item count, likes.            |
| `fetch_list_items`     | Items on a list.                                       |
| `fetch_trending_lists` | Lists people are liking now.                           |
| `fetch_popular_lists`  | Most popular lists.                                    |
| `create_list`          | Creates a list on your account.                        |
| `update_list`          | Renames a list or changes its description and privacy. |
| `add_list_items`       | Adds up to 50 items to one of your lists.              |
| `remove_list_items`    | Removes items from one of your lists.                  |
| `delete_list`          | Deletes one of your lists.                             |

### Watchlist edits, notes and favorites (12)

| Tool                    | What it does                                                            |
| ----------------------- | ----------------------------------------------------------------------- |
| `update_watchlist_item` | Sets or clears the note of a watchlist entry (VIP only).                |
| `reorder_watchlist`     | Reorders your whole watchlist by `list_item_id` (VIP only).             |
| `fetch_note`            | One of your notes (VIP only).                                           |
| `fetch_user_notes`      | Your notes, optionally for one kind of item (VIP only).                 |
| `create_note`           | Attaches a note to a movie, show, season, episode or person (VIP only). |
| `update_note`           | Edits a note (VIP only).                                                |
| `delete_note`           | Deletes a note (VIP only).                                              |
| `fetch_favorites`       | Your favorite movies and shows.                                         |
| `add_to_favorites`      | Adds items to your favorites.                                           |
| `update_favorite`       | Changes the note on a favorite.                                         |
| `reorder_favorites`     | Reorders your favorites.                                                |
| `remove_from_favorites` | Removes items from your favorites.                                      |

### Progress and up next (7)

| Tool                             | What it does                                                                   |
| -------------------------------- | ------------------------------------------------------------------------------ |
| `fetch_show_collection_progress` | How much of a show you collected: counts, percentage, next episode to collect. |
| `reset_show_progress`            | Restarts a show's watch progress to rewatch it (VIP only).                     |
| `undo_reset_show_progress`       | Undoes the last reset (VIP only).                                              |
| `fetch_up_next`                  | The next episode to watch in each show you follow.                             |
| `fetch_hidden_progress`          | Shows and seasons hidden from your progress.                                   |
| `hide_from_progress`             | Hides a show or season from your progress.                                     |
| `unhide_from_progress`           | Shows a hidden show or season again.                                           |

Notes, watchlist notes and show progress reset need Trakt VIP; other accounts get "Trakt VIP is required for this feature".

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
