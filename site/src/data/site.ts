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

export type ToolGroup = 'history' | 'library' | 'checkin' | 'shows' | 'episodes' | 'movies' | 'people';

export type Tool = {
  /** MCP tool name, exactly as registered in internal/tools; scripts/check-dist.mjs compares them. */
  name: string;
  /** What it does, in the user's words. */
  summary: string;
  group: ToolGroup;
};

export const TOOLS: readonly Tool[] = [
  // Your watched items and progress.
  { name: 'fetch_user_watched', summary: 'List the movies or shows you watched, with last watched date and play count.', group: 'history' },
  { name: 'fetch_history', summary: 'Read your watch history, optionally for one item or a date range.', group: 'history' },
  { name: 'add_to_history', summary: 'Mark movies, shows, seasons or episodes as watched.', group: 'history' },
  { name: 'remove_from_history', summary: 'Remove items from your watch history.', group: 'history' },
  { name: 'fetch_show_progress', summary: 'See how far you are in a show and what comes next.', group: 'history' },
  { name: 'fetch_playback_progress', summary: 'See what you paused halfway.', group: 'history' },
  { name: 'remove_playback_item', summary: 'Clear a paused playback entry.', group: 'history' },

  // Watchlist, collection, ratings, smart lists.
  { name: 'fetch_watchlist', summary: 'Read your watchlist, filtered by type and sorted as you like.', group: 'library' },
  { name: 'add_to_watchlist', summary: 'Add items to your watchlist, with a note if you have Trakt VIP.', group: 'library' },
  { name: 'remove_from_watchlist', summary: 'Remove items from your watchlist.', group: 'library' },
  { name: 'fetch_collection', summary: 'Read the movies and shows in your collection.', group: 'library' },
  { name: 'add_to_collection', summary: 'Add items to your collection.', group: 'library' },
  { name: 'remove_from_collection', summary: 'Remove items from your collection.', group: 'library' },
  { name: 'fetch_ratings', summary: 'Read the ratings you gave.', group: 'library' },
  { name: 'add_ratings', summary: 'Rate movies, shows, seasons or episodes from 1 to 10.', group: 'library' },
  { name: 'remove_ratings', summary: 'Remove ratings you gave.', group: 'library' },
  { name: 'fetch_smart_lists', summary: 'Read your smart lists and the filters behind each.', group: 'library' },
  { name: 'fetch_smart_list_items', summary: 'See the movies and shows a smart list holds right now.', group: 'library' },

  // Check-ins and recommendations.
  { name: 'checkin_to_show', summary: 'Check in to the episode you are watching now.', group: 'checkin' },
  { name: 'checkin_to_movie', summary: 'Check in to the movie you are watching now.', group: 'checkin' },
  { name: 'cancel_checkin', summary: 'Cancel your active check-in.', group: 'checkin' },
  { name: 'fetch_movie_recommendations', summary: 'Get movie recommendations, skipping what you collected or listed.', group: 'checkin' },
  { name: 'fetch_show_recommendations', summary: 'Get show recommendations, skipping what you collected or listed.', group: 'checkin' },
  { name: 'hide_movie_recommendation', summary: 'Stop recommending a movie.', group: 'checkin' },
  { name: 'hide_show_recommendation', summary: 'Stop recommending a show.', group: 'checkin' },
  { name: 'unhide_movie_recommendation', summary: 'Bring a hidden movie recommendation back.', group: 'checkin' },
  { name: 'unhide_show_recommendation', summary: 'Bring a hidden show recommendation back.', group: 'checkin' },

  // Catalog: shows.
  { name: 'fetch_trending_shows', summary: 'Shows people are watching right now.', group: 'shows' },
  { name: 'fetch_popular_shows', summary: 'The most popular shows.', group: 'shows' },
  { name: 'fetch_anticipated_shows', summary: 'The most anticipated upcoming shows.', group: 'shows' },
  { name: 'fetch_favorited_shows', summary: 'The most favorited shows over a period.', group: 'shows' },
  { name: 'fetch_played_shows', summary: 'The most played shows over a period.', group: 'shows' },
  { name: 'fetch_watched_shows', summary: 'The most watched shows over a period.', group: 'shows' },
  { name: 'search_shows', summary: 'Find a show by title.', group: 'shows' },
  { name: 'fetch_show_ratings', summary: 'Ratings and vote distribution of a show.', group: 'shows' },
  { name: 'fetch_show_summary', summary: 'Details of one show.', group: 'shows' },
  { name: 'fetch_show_videos', summary: 'Trailers and clips of a show.', group: 'shows' },
  { name: 'fetch_related_shows', summary: 'Shows similar to one you like.', group: 'shows' },
  { name: 'fetch_show_seasons', summary: 'The seasons of a show.', group: 'shows' },
  { name: 'fetch_show_people', summary: 'Cast and crew of a show.', group: 'shows' },

  // Catalog: seasons and episodes.
  { name: 'fetch_season_info', summary: 'Details of one season.', group: 'episodes' },
  { name: 'fetch_season_episodes', summary: 'The episodes of a season.', group: 'episodes' },
  { name: 'fetch_season_ratings', summary: 'Ratings of a season.', group: 'episodes' },
  { name: 'fetch_season_stats', summary: 'Watcher, play and collection counts of a season.', group: 'episodes' },
  { name: 'fetch_season_people', summary: 'Cast and crew of a season.', group: 'episodes' },
  { name: 'fetch_season_videos', summary: 'Trailers and clips of a season.', group: 'episodes' },
  { name: 'fetch_season_watching', summary: 'Who is watching a season right now.', group: 'episodes' },
  { name: 'fetch_season_translations', summary: 'Translated titles and overviews of a season.', group: 'episodes' },
  { name: 'fetch_season_lists', summary: 'Public lists that contain a season.', group: 'episodes' },
  { name: 'fetch_season_comments', summary: 'Comments on a season.', group: 'episodes' },
  { name: 'fetch_episode_summary', summary: 'Details of one episode.', group: 'episodes' },
  { name: 'fetch_episode_ratings', summary: 'Ratings of an episode.', group: 'episodes' },
  { name: 'fetch_episode_stats', summary: 'Watcher, play and collection counts of an episode.', group: 'episodes' },
  { name: 'fetch_episode_people', summary: 'Cast and crew of an episode.', group: 'episodes' },
  { name: 'fetch_episode_videos', summary: 'Trailers and clips of an episode.', group: 'episodes' },
  { name: 'fetch_episode_watching', summary: 'Who is watching an episode right now.', group: 'episodes' },
  { name: 'fetch_episode_translations', summary: 'Translated titles and overviews of an episode.', group: 'episodes' },
  { name: 'fetch_episode_lists', summary: 'Public lists that contain an episode.', group: 'episodes' },
  { name: 'fetch_episode_comments', summary: 'Comments on an episode.', group: 'episodes' },

  // Catalog: movies.
  { name: 'fetch_trending_movies', summary: 'Movies people are watching right now.', group: 'movies' },
  { name: 'fetch_popular_movies', summary: 'The most popular movies.', group: 'movies' },
  { name: 'fetch_anticipated_movies', summary: 'The most anticipated upcoming movies.', group: 'movies' },
  { name: 'fetch_favorited_movies', summary: 'The most favorited movies over a period.', group: 'movies' },
  { name: 'fetch_played_movies', summary: 'The most played movies over a period.', group: 'movies' },
  { name: 'fetch_watched_movies', summary: 'The most watched movies over a period.', group: 'movies' },
  { name: 'fetch_boxoffice_movies', summary: 'This weekend’s top box office movies.', group: 'movies' },
  { name: 'search_movies', summary: 'Find a movie by title.', group: 'movies' },
  { name: 'fetch_movie_ratings', summary: 'Ratings and vote distribution of a movie.', group: 'movies' },
  { name: 'fetch_movie_summary', summary: 'Details of one movie.', group: 'movies' },
  { name: 'fetch_movie_videos', summary: 'Trailers and clips of a movie.', group: 'movies' },
  { name: 'fetch_related_movies', summary: 'Movies similar to one you like.', group: 'movies' },
  { name: 'fetch_movie_people', summary: 'Cast and crew of a movie.', group: 'movies' },
  { name: 'fetch_movie_comments', summary: 'Comments on a movie.', group: 'movies' },

  // Catalog: people and comments.
  { name: 'fetch_person_summary', summary: 'Details of an actor, director or crew member.', group: 'people' },
  { name: 'fetch_person_movies', summary: 'The movies a person worked on.', group: 'people' },
  { name: 'fetch_person_shows', summary: 'The shows a person worked on.', group: 'people' },
  { name: 'fetch_show_comments', summary: 'Comments on a show, spoilers hidden by default.', group: 'people' },
  { name: 'fetch_comment', summary: 'Read one comment.', group: 'people' },
  { name: 'fetch_comment_replies', summary: 'Read the replies to a comment.', group: 'people' },
];

export const TOOL_GROUPS: readonly { group: ToolGroup; title: string; description: string }[] = [
  { group: 'history', title: 'What you watched', description: 'Log, review and undo your watch history, and see your progress in a show.' },
  {
    group: 'library',
    title: 'Your lists and ratings',
    description: 'Watchlist, collection, ratings and smart lists: read them, add to them, remove from them.',
  },
  {
    group: 'checkin',
    title: 'Check in and discover',
    description: 'Check in to what you are watching now, and get recommendations you can hide.',
  },
  { group: 'shows', title: 'Browse shows', description: 'Trending, popular and anticipated shows, search, details, ratings and cast.' },
  { group: 'episodes', title: 'Seasons and episodes', description: 'Episode lists, ratings, stats, cast, videos, translations and comments.' },
  { group: 'movies', title: 'Browse movies', description: 'Trending, popular and box office movies, search, details, ratings and cast.' },
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
