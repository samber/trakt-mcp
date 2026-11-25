/** Public origin of the website; canonical URLs and the sitemap derive from it. Placeholder until the hostname is confirmed. */
export const SITE_URL = 'https://trakt-mcp.samber.dev';

/** Product name, shown in titles, structured data and llms.txt. */
export const SITE_NAME = 'Trakt MCP';

/** One-sentence description of the service, reused by structured data and llms.txt. */
export const SITE_SUMMARY =
  'A remote MCP server that lets Claude and ChatGPT browse the Trakt catalog and manage your watchlist, watch history, collection, ratings and check-ins, with your consent.';

/** Connector URL users paste into Claude or ChatGPT: the MCP server's endpoint. */
export const MCP_URL = 'https://mcp.trakt-mcp.samber.dev/mcp';

/** Source repository of the open-source server. */
export const REPO_URL = 'https://github.com/samber/trakt-mcp';

/** Person who operates the hosted instance, named in the policy and the terms. */
export const OPERATOR_NAME = 'Samuel Berthe';

/** The operator's LinkedIn profile, linked from the operator's name in the footer. */
export const OPERATOR_LINKEDIN = 'https://www.linkedin.com/in/samuelberthe';

/** The operator's GitHub profile, the owner of the repository above. */
export const OPERATOR_GITHUB = 'https://github.com/samber';

/** Documentation in the repository, linked from llms.txt and structured data. */
export const SELF_HOSTING_URL = `${REPO_URL}/blob/main/docs/self-hosting.md`;
export const SECURITY_URL = `${REPO_URL}/blob/main/docs/security.md`;

/** Where a Trakt user revokes the application (Settings, Connected Apps). */
export const TRAKT_CONNECTED_APPS_URL = 'https://trakt.tv/oauth/authorized_applications';

/** Trakt's own privacy policy: Trakt is the source and processor of the viewing data. */
export const TRAKT_PRIVACY_URL = 'https://trakt.tv/privacy';

/** Days of inactivity after which the server purges an account. Matches `inactivityWindow` in internal/jobs. */
export const INACTIVITY_DAYS = 180;

/** Public contact address for privacy and terms questions. */
export const OPERATOR_EMAIL = 'hello@samuel-berthe.fr';

/** Branding rule: "Trakt" is used descriptively only. This sentence appears on the home page, the footer and the legal pages. */
export const NOT_AFFILIATED =
  'Trakt MCP is an independent open source project, not affiliated with or endorsed by Trakt.';

export type ToolGroup =
  | 'history'
  | 'library'
  | 'lists'
  | 'notes'
  | 'checkin'
  | 'calendars'
  | 'lookup'
  | 'charts'
  | 'details'
  | 'people';

export type Tool = {
  /** MCP tool name, exactly as registered in internal/tools; scripts/check-dist.mjs compares them. */
  name: string;
  /** What it does, in the user's words. */
  summary: string;
  group: ToolGroup;
};

export const TOOLS: readonly Tool[] = [
  // Your watched items and progress.
  { name: 'list_user_watched', summary: 'List the movies or shows you watched, with last watched date and play count.', group: 'history' },
  { name: 'list_history', summary: 'Read your watch history, optionally for one item or a date range.', group: 'history' },
  { name: 'add_to_history', summary: 'Mark movies, shows, seasons or episodes as watched.', group: 'history' },
  { name: 'remove_from_history', summary: 'Remove items from your watch history.', group: 'history' },
  { name: 'get_show_progress', summary: 'See how far you are in a show and what comes next.', group: 'history' },
  { name: 'list_playback_progress', summary: 'See what you paused halfway.', group: 'history' },
  { name: 'remove_playback_item', summary: 'Clear a paused playback entry.', group: 'history' },
  { name: 'get_show_collection_progress', summary: 'See how much of a show you have collected and what to collect next.', group: 'history' },
  { name: 'reset_show_progress', summary: 'Restart a show from now to rewatch it (Trakt VIP).', group: 'history' },
  { name: 'undo_reset_show_progress', summary: 'Undo the last reset of a show so earlier plays count again (Trakt VIP).', group: 'history' },
  { name: 'list_up_next', summary: 'See the shows you are watching, each with its next episode to watch.', group: 'history' },
  { name: 'list_hidden_progress', summary: 'See the shows and seasons you hid from your progress.', group: 'history' },
  { name: 'hide_from_progress', summary: 'Hide shows or seasons from your progress and up next.', group: 'history' },
  { name: 'unhide_from_progress', summary: 'Bring hidden shows or seasons back into your progress.', group: 'history' },

  // Watchlist, collection, ratings, smart lists.
  { name: 'list_watchlist', summary: 'Read your watchlist, filtered by type and sorted as you like.', group: 'library' },
  { name: 'add_to_watchlist', summary: 'Add items to your watchlist, with a note if you have Trakt VIP.', group: 'library' },
  { name: 'remove_from_watchlist', summary: 'Remove items from your watchlist.', group: 'library' },
  { name: 'list_collection', summary: 'Read the movies and shows in your collection.', group: 'library' },
  { name: 'add_to_collection', summary: 'Add items to your collection.', group: 'library' },
  { name: 'remove_from_collection', summary: 'Remove items from your collection.', group: 'library' },
  { name: 'list_user_ratings', summary: 'Read the ratings you gave.', group: 'library' },
  { name: 'add_ratings', summary: 'Rate movies, shows, seasons or episodes from 1 to 10.', group: 'library' },
  { name: 'remove_ratings', summary: 'Remove ratings you gave.', group: 'library' },
  { name: 'list_smart_lists', summary: 'Read your smart lists and the filters behind each.', group: 'library' },
  { name: 'list_smart_list_items', summary: 'See the movies and shows a smart list holds right now.', group: 'library' },
  { name: 'update_watchlist_item', summary: 'Set or clear the note on a watchlist entry (Trakt VIP).', group: 'library' },
  { name: 'reorder_watchlist', summary: 'Put your watchlist in the order you want.', group: 'library' },

  // Custom lists.
  { name: 'list_user_lists', summary: 'Read your custom lists, or the public lists of another user.', group: 'lists' },
  { name: 'get_list', summary: 'Read one custom list: name, privacy and item count.', group: 'lists' },
  { name: 'list_list_items', summary: 'See the movies, shows and people on a list.', group: 'lists' },
  { name: 'list_lists', summary: 'Lists people are liking right now, or the most popular lists (trending or popular).', group: 'lists' },
  { name: 'create_list', summary: 'Create a custom list (privacy: private, link, friends or public).', group: 'lists' },
  { name: 'update_list', summary: 'Rename a list or change its description and privacy (private, link, friends or public).', group: 'lists' },
  { name: 'add_list_items', summary: 'Add movies, shows, seasons or episodes to a list.', group: 'lists' },
  { name: 'remove_list_items', summary: 'Remove items from a list.', group: 'lists' },
  { name: 'delete_list', summary: 'Delete a custom list.', group: 'lists' },

  // Notes (free accounts keep few) and favorites.
  { name: 'get_note', summary: 'Read one of your notes (free accounts keep few notes).', group: 'notes' },
  { name: 'list_user_notes', summary: 'List your notes, optionally for one kind of item (free accounts keep few notes).', group: 'notes' },
  { name: 'create_note', summary: 'Attach a note to a movie, show, season, episode or person (free accounts keep few notes).', group: 'notes' },
  { name: 'update_note', summary: 'Edit a note (free accounts keep few notes).', group: 'notes' },
  { name: 'delete_note', summary: 'Delete a note (free accounts keep few notes).', group: 'notes' },
  { name: 'list_favorites', summary: 'Read your favorite movies and shows.', group: 'notes' },
  { name: 'add_to_favorites', summary: 'Add movies or shows to your favorites, with an optional note.', group: 'notes' },
  { name: 'update_favorite', summary: 'Set or clear the note on a favorite, or move it to a new rank.', group: 'notes' },
  { name: 'reorder_favorites', summary: 'Put your favorites in the order you want.', group: 'notes' },
  { name: 'remove_from_favorites', summary: 'Remove items from your favorites.', group: 'notes' },

  // Check-ins and recommendations.
  { name: 'checkin', summary: 'Check in to the movie or episode you are watching now.', group: 'checkin' },
  { name: 'cancel_checkin', summary: 'Cancel your active check-in.', group: 'checkin' },
  { name: 'list_recommendations', summary: 'Get movie or show recommendations, skipping what you collected or listed.', group: 'checkin' },
  { name: 'hide_recommendation', summary: 'Stop recommending a movie or show.', group: 'checkin' },
  { name: 'unhide_recommendation', summary: 'Bring a hidden movie or show recommendation back.', group: 'checkin' },

  // Calendars and where to watch.
  { name: 'list_calendar', summary: 'What airs or is released in a date window: episodes, new shows, premieres, finales, movies, DVD or streaming.', group: 'calendars' },
  { name: 'get_watchnow', summary: 'Where to watch a movie, show or episode in your country.', group: 'calendars' },
  { name: 'list_watchnow_sources', summary: 'Every streaming provider Trakt knows.', group: 'calendars' },

  // Search by id and reference data.
  { name: 'search', summary: 'Find a movie or show by title, or by its exact title.', group: 'lookup' },
  { name: 'search_by_id', summary: 'Find a title or person from its Trakt, IMDb, TMDB or TVDB id.', group: 'lookup' },
  { name: 'list_genres', summary: 'List the genres (name and slug) Trakt uses for movies or shows.', group: 'lookup' },
  { name: 'list_countries', summary: 'List the countries (name and code) Trakt uses for movies or shows.', group: 'lookup' },
  { name: 'list_languages', summary: 'List the languages (name and code) Trakt uses for movies or shows.', group: 'lookup' },
  { name: 'list_certifications', summary: 'List the content ratings Trakt uses, for every country.', group: 'lookup' },
  { name: 'list_networks', summary: 'List the TV networks Trakt knows.', group: 'lookup' },

  // Catalog: charts.
  { name: 'list_trending', summary: 'Movies or shows people are watching right now.', group: 'charts' },
  { name: 'list_popular', summary: 'The most popular movies or shows.', group: 'charts' },
  { name: 'list_anticipated', summary: 'The most anticipated upcoming movies or shows.', group: 'charts' },
  { name: 'list_favorited', summary: 'The most favorited movies or shows over a period.', group: 'charts' },
  { name: 'list_played', summary: 'The most played movies or shows over a period.', group: 'charts' },
  { name: 'list_watched', summary: 'The most watched movies or shows over a period.', group: 'charts' },
  { name: 'list_boxoffice', summary: 'This weekend’s top box office movies.', group: 'charts' },
  { name: 'list_related', summary: 'Movies or shows similar to one you like.', group: 'charts' },

  // Catalog: details of a movie, show, season or episode.
  { name: 'get_summary', summary: 'Details of a movie, show, season or episode.', group: 'details' },
  { name: 'get_ratings', summary: 'Ratings and vote distribution of a movie, show, season or episode.', group: 'details' },
  { name: 'get_stats', summary: 'Watcher, play and collection counts of a movie, show, season or episode.', group: 'details' },
  { name: 'list_watching', summary: 'Who is watching a movie, show, season or episode right now.', group: 'details' },
  { name: 'list_people', summary: 'Cast and crew of a movie, show, season or episode.', group: 'details' },
  { name: 'list_videos', summary: 'Trailers and clips of a movie, show, season or episode.', group: 'details' },
  { name: 'list_translations', summary: 'Translated titles and overviews of a movie, show, season or episode.', group: 'details' },
  { name: 'list_item_lists', summary: 'Public lists that contain a movie, show, season or episode.', group: 'details' },
  { name: 'list_aliases', summary: 'Alternative titles of a movie or show.', group: 'details' },
  { name: 'list_studios', summary: 'Studios behind a movie or show.', group: 'details' },
  { name: 'list_seasons', summary: 'The seasons of a show.', group: 'details' },
  { name: 'list_season_episodes', summary: 'The episodes of a season.', group: 'details' },
  { name: 'get_next_episode', summary: 'The next episode of a show to air.', group: 'details' },
  { name: 'get_last_episode', summary: 'The latest episode of a show that aired.', group: 'details' },
  { name: 'list_show_certifications', summary: 'Age ratings of a show by country.', group: 'details' },
  { name: 'list_movie_releases', summary: 'Release dates of a movie by country.', group: 'details' },

  // Catalog: people and comments.
  { name: 'get_person', summary: 'Details of an actor, director or crew member.', group: 'people' },
  { name: 'list_person_credits', summary: 'The movies or shows a person worked on.', group: 'people' },
  { name: 'list_comments', summary: 'Comments on a movie, show, season or episode, spoilers hidden by default.', group: 'people' },
  { name: 'get_comment', summary: 'Read one comment.', group: 'people' },
  { name: 'list_comment_replies', summary: 'Read the replies to a comment.', group: 'people' },
];

export const TOOL_GROUPS: readonly { group: ToolGroup; title: string; description: string }[] = [
  {
    group: 'history',
    title: 'What you watched',
    description: 'Log, review and undo your watch history, track progress, see what is up next and restart a show.',
  },
  {
    group: 'library',
    title: 'Your lists and ratings',
    description: 'Watchlist, collection, ratings and smart lists: read them, add to them, remove from them, reorder the watchlist.',
  },
  {
    group: 'lists',
    title: 'Custom lists',
    description: 'Browse, create, edit and delete lists, and see which lists are trending or popular.',
  },
  {
    group: 'notes',
    title: 'Notes and favorites',
    description: 'Keep notes on titles and people and curate your favorites.',
  },
  {
    group: 'checkin',
    title: 'Check in and discover',
    description: 'Check in to what you are watching now, and get recommendations you can hide.',
  },
  {
    group: 'calendars',
    title: 'Calendars and where to watch',
    description: 'What airs or releases when, and which streaming services carry a title.',
  },
  {
    group: 'lookup',
    title: 'Find and look up',
    description: 'Search by title or id, and list genres, countries, languages, ratings and networks.',
  },
  { group: 'charts', title: 'Trending and popular', description: 'Trending, popular, anticipated and box office movies and shows, and similar titles.' },
  {
    group: 'details',
    title: 'Movies, shows and episodes',
    description: 'Details, ratings, stats, cast, videos, translations, seasons, episodes and release dates.',
  },
  { group: 'people', title: 'People and comments', description: 'Actors and crew, and what the community says.' },
];

/**
 * What the server keeps, in plain words for non-technical visitors. Same facts as README
 * the privacy policy, which give the technical detail.
 */
export const STORED: readonly string[] = [
  'Your Trakt user id and username slug',
  'Your Trakt access and tokens, encrypted',
  'Which assistant apps connected, such as Claude or ChatGPT',
];

/** What the server never keeps. Mirrors the privacy policy and docs/self-hosting.md "What the server stores". */
export const NEVER_STORED: readonly string[] = [
  'Your watch history, watchlist, ratings and collection',
  'Comments, and the tool inputs and outputs exchanged with your assistant',
  'Watch history, ratings, tool arguments or results in logs at the default log level',
];

export type FaqEntry = {
  question: string;
  /** Plain text: the same string feeds the page, the FAQPage structured data and llms.txt. */
  answer: string;
};

/** Questions people ask before connecting. Every answer restates a fact from the README, the privacy policy or the code. */
export const FAQ: readonly FaqEntry[] = [
  {
    question: 'Can Claude access my Trakt account?',
    answer:
      'Yes. Add the connector URL to Claude as a custom connector, sign in with Trakt and approve access. Claude can then browse the Trakt catalog and read and change your watchlist, history, collection and ratings. You can revoke access at any time in Trakt, under Settings, Connected Apps.',
  },
  {
    question: 'Does it work with ChatGPT?',
    answer:
      'Yes. Turn on developer mode in ChatGPT, create an app with the same connector URL and sign in with Trakt. It works the same way as in Claude.',
  },
  {
    question: 'Is it free?',
    answer:
      'Yes. The hosted service is free, with no promise of availability, and the server is open source under the MIT license. You can also run your own copy. Trakt keeps its own rules: some features, such as notes on watchlist items, need Trakt VIP, and Trakt caps the size of lists, watchlists and collections on free accounts.',
  },
  {
    question: 'Does it store my watch history?',
    answer:
      'No. Every tool call reads or writes Trakt directly. The server keeps only what it needs to keep you signed in: your Trakt user id and username slug, your Trakt tokens (encrypted), and a fingerprint of the pass your assistant uses. Your history, watchlist, ratings and collection are never stored, and tool arguments or results are not logged at the default log level.',
  },
  {
    question: 'Can it change or delete things on Trakt?',
    answer:
      'Yes. The write tools change your real Trakt account: they log plays, edit your watchlist and collection, and set or remove ratings. The tools that remove or clear something are flagged as destructive, and the tools that only read are flagged as read-only. A change made through the connector is a change at Trakt.',
  },
  {
    question: 'How do I disconnect and erase my data?',
    answer: `Disconnect the connector in your assistant, and revoke the application in Trakt under Settings, Connected Apps. After a revoke at Trakt, the server deletes your data the next time your assistant calls a tool, or when the ${INACTIVITY_DAYS}-day purge of unused accounts runs, so deletion can take up to ${INACTIVITY_DAYS} days. To have everything deleted at once, e-mail the operator at ${OPERATOR_EMAIL}.`,
  },
  {
    question: 'Is it affiliated with Trakt?',
    answer:
      'No. Trakt MCP is an independent open source project, not affiliated with or endorsed by Trakt. The name Trakt is used only to say which service it connects to.',
  },
];

/** Date (YYYY-MM-DD) of the last change to the privacy policy or the terms. Update it with every edit of either page. */
export const LEGAL_UPDATED = '2026-11-24';
