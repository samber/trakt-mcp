import { defineConfig } from 'astro/config';
import sitemap from '@astrojs/sitemap';
import { LEGAL_UPDATED, SITE_URL } from './src/data/site.ts';

// SITE is the public origin; Astro builds canonical URLs and the sitemap from it.
const SITE = SITE_URL;

export default defineConfig({
  site: SITE,
  output: 'static',
  // "/privacy" and "/terms" are the canonical paths, with no trailing slash:
  // the privacy URL registered with Trakt and the assistant directories must stay valid.
  trailingSlash: 'never',
  build: { format: 'file' },
  integrations: [
    sitemap({
      // Only the legal pages have a known change date; the home page gets none rather than a build date that would claim fresh content on every build.
      serialize(item) {
        const path = new URL(item.url).pathname;
        if (path === '/privacy' || path === '/terms') item.lastmod = LEGAL_UPDATED;
        return item;
      },
    }),
  ],
  vite: {
    build: {
      // The default minifier folds `animation` + `animation-timeline: view()` into one
      // shorthand ("animation: linear both fill view()") that Chrome rejects, which drops
      // every scroll-linked animation. esbuild keeps the longhands as written.
      cssMinify: 'esbuild',
    },
  },
});
