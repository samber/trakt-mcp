import { REPO_URL } from './site';

/** GitHub REST endpoint of the repository, derived from REPO_URL. */
const REPO_API_URL = REPO_URL.replace('https://github.com/', 'https://api.github.com/repos/');

/** A slow or unreachable API must not stall the build: give up after 5 s. */
const FETCH_TIMEOUT_MS = 5000;

/** From this count on, the label is shortened to thousands ("1.2k"). */
const THOUSAND = 1000;

/**
 * Star count of the repository, read once at build time so visitors' browsers
 * never call GitHub (the privacy policy promises no third-party request).
 * Returns null when the count is unknown: offline build, private repository,
 * rate limit. The page then shows the GitHub link without a count.
 */
export function fetchStarCount(): Promise<number | null> {
  // Every page renders the header: one request per build, shared by all pages.
  starCount ??= requestStarCount();
  return starCount;
}

let starCount: Promise<number | null> | undefined;

async function requestStarCount(): Promise<number | null> {
  try {
    const response = await fetch(REPO_API_URL, {
      headers: { Accept: 'application/vnd.github+json' },
      signal: AbortSignal.timeout(FETCH_TIMEOUT_MS),
    });
    if (!response.ok) {
      // bearer:disable javascript_lang_logger_leak
      console.warn(`[github] star count unavailable: ${REPO_API_URL} answered ${response.status}`);
      return null;
    }
    const body: unknown = await response.json();
    const stars = (body as { stargazers_count?: unknown }).stargazers_count;
    if (typeof stars !== 'number') {
      console.warn('[github] star count unavailable: no stargazers_count in the response');
      return null;
    }
    return stars;
  } catch (error) {
    // bearer:disable javascript_lang_logger_leak
    console.warn(`[github] star count unavailable: ${error instanceof Error ? error.message : String(error)}`);
    return null;
  }
}

/** "842", "1.2k", "12k". */
export function formatStarCount(stars: number): string {
  if (stars < THOUSAND) return String(stars);
  const thousands = stars / THOUSAND;
  return `${thousands < 10 ? thousands.toFixed(1).replace(/\.0$/, '') : Math.round(thousands)}k`;
}
