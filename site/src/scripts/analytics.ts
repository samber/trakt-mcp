export type AnalyticsEvent = 'connector_url_copy' | 'github_click';

type AnalyticsTarget = {
  dataLayer?: unknown[];
  posthog?: { capture?: (event: string) => void };
};

/**
 * Reports a named event to the tag manager data layer and, when loaded, PostHog.
 * Carries no payload beyond the name. Never throws: with analytics absent
 * (development, blocked scripts) it does nothing.
 */
export function trackEvent(name: AnalyticsEvent, target: object = globalThis): void {
  const analytics = target as AnalyticsTarget;
  try {
    analytics.dataLayer = analytics.dataLayer ?? [];
    analytics.dataLayer.push({ event: name });
  } catch {
    // A page script replaced dataLayer with something unusable: skip it.
  }
  try {
    analytics.posthog?.capture?.(name);
  } catch {
    // PostHog stub or blocker misbehaving: never break the page.
  }
}
