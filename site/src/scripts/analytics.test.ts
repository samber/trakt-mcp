import assert from 'node:assert/strict';
import { test } from 'node:test';
import { trackEvent } from './analytics.ts';

test('trackEvent pushes the event on dataLayer, creating it when missing', () => {
  const target: { dataLayer?: unknown[] } = {};
  trackEvent('github_click', target);
  assert.deepEqual(target.dataLayer, [{ event: 'github_click' }]);
});

test('trackEvent appends to an existing dataLayer', () => {
  const target = { dataLayer: [{ event: 'gtm.js' }] };
  trackEvent('connector_url_copy', target);
  assert.deepEqual(target.dataLayer, [{ event: 'gtm.js' }, { event: 'connector_url_copy' }]);
});

test('trackEvent captures the same name in PostHog when loaded', () => {
  const captured: string[] = [];
  trackEvent('connector_url_copy', { posthog: { capture: (name: string) => captured.push(name) } });
  assert.deepEqual(captured, ['connector_url_copy']);
});

test('trackEvent does not throw when PostHog capture throws', () => {
  const posthog = {
    capture: () => {
      throw new Error('blocked');
    },
  };
  assert.doesNotThrow(() => trackEvent('github_click', { posthog }));
});

test('trackEvent does not throw when dataLayer is unusable', () => {
  assert.doesNotThrow(() => trackEvent('github_click', { dataLayer: 'broken' }));
});

test('trackEvent does not throw when PostHog has no capture method yet', () => {
  assert.doesNotThrow(() => trackEvent('github_click', { posthog: {} }));
});
