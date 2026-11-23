# site

## Purpose

- Static Astro website of the hosted trakt-mcp: landing page, privacy policy, terms of service.
- Plain CSS, no Tailwind, no UI framework. One page script: the copy button.
- Design: Stripe-inspired (owner's chosen reference). White page, navy ink, a cobalt-to-mint accent gradient (`--g1`..`--g5`): an animated diagonal ribbon in the hero, corner washes on tiles, one navy closing section. Headlines pair a statement with a grey `.soft` second half. Schibsted Grotesk plus the system monospace, 16px/8px radii, light and dark from `prefers-color-scheme`. The index-card look of the original spec (`docs/superpowers/specs/2026-11-20-landing-site-design.md`) is retired; its content and gate rules still apply.
- CSS is minified with esbuild (`vite.build.cssMinify` in `astro.config.mjs`, `esbuild` dev dependency). The default minifier folds `animation` + `animation-timeline: view()` into a shorthand Chrome rejects, silently dropping every scroll-linked animation. Never name keyframes `marker`.
- Motion is CSS only, each with a purpose: hero load-in, the chat demo playing once, the ribbon drifting, tile demos (typing, field filling, labels) driven by scroll-driven animations (`animation-timeline: view()`) behind `@supports`. Reduced motion and unsupported browsers show the final state.
- Token names in `src/styles/tokens.css` are fixed by `scripts/check-contrast.mjs`: `--red` is the "never kept" red, `--mark` the pale mint for fields the assistant wrote; `--mint` is the solid secondary colour (buttons, badges). The logo (`public/favicon.svg`, film frame with an amber play triangle) uses navy and amber; the owner approved amber as the accent for the logo and its derived images only. The page palette keeps its tokens.
- The connector URL box (`ConnectorUrl.astro`) appears twice (hero, closing step 1): give each instance a unique `id`; the copy script binds every `[data-copy-root]`.
- The header's GitHub star count is fetched once at build time (`src/data/github.ts`); visitors' browsers never call GitHub. It is hidden while the repository is private or the API is unreachable.
- `public/manifest.json` and the 96/192/512 px PNG icons come from `node scripts/icons.mjs` (reads `public/favicon.svg`). `public/<key>.txt` is the IndexNow key file: its name and content are the same key, and it must stay served at the site root.
- The GitHub logo comes from the `simple-icons` dev dependency, inlined at build time. Never use the Trakt logo; the footer and legal pages say the project is not affiliated with or endorsed by Trakt (`NOT_AFFILIATED` in `src/data/site.ts`).
- Check every visual change in a real browser before reporting it: `astro preview`, then screenshots. Gates do not catch white text on white cards or missing spaces before inline spans (`{' '}`).
- Demo data is generic: public movie and series titles and example ratings, never a real viewing history. The tool list (`TOOLS` in `src/data/site.ts`, 80 names) must equal the registrations in `internal/tools` (`check-dist.mjs` compares them). Tool arguments shown in demos must match the JSON names in `internal/tools`.

## Domains

| Role                     | URL                                                   |
| ------------------------ | ----------------------------------------------------- |
| Website                  | `https://trakt-mcp.samber.dev` (placeholder, `SITE_URL`) |
| MCP server, OAuth issuer | `https://mcp.trakt-mcp.samber.dev` (`/mcp`, `MCP_URL`)   |

## Commands

Run from `site/`. Set `ASTRO_TELEMETRY_DISABLED=1` (no tracking; the sandbox also blocks the telemetry directory).

```sh
npm run check    # astro check (types)
npm test         # unit tests of the page scripts
npm run build    # static build into dist/
npm run verify   # contrast of the tokens + the dist gate (run after build)
node scripts/og.mjs  # regenerate public/og.png by hand when the design changes
```

## Gates

Run `check`, `test`, `build`, `verify` before every commit that touches `site/`.

- `scripts/check-contrast.mjs`: WCAG contrast of the palette tokens, light and dark.
- `scripts/check-dist.mjs`: built pages, canonical URLs, one `h1`, footer links, no external host, JSON-LD, sitemap, `robots.txt`, `llms.txt`, `og.png`, scripts under 1.5 KB gzipped, tool list equal to `internal/tools`, privacy and terms requirements (including the "deletion can take up to 180 days" sentence), no "Google" or "contacts" in visible text, Trakt in every title.
- Lighthouse on `/` and `/privacy` is a manual pre-launch check: target above 90 on all four scores.

## Rules

- Facts on the site must match the code and spec section 10 and 11 (`docs/superpowers/specs/2026-11-23-trakt-mcp-design.md`): tools (`internal/tools`), what is stored (README, `internal/store`), retention (`internal/jobs`).
- Revoking at Trakt does not delete our rows at once: they go at the next failed call or the 180-day purge. Keep the privacy page saying so.
- Example data is fictional. Never the owner's or a real person's data. The only real profile link is the operator's own LinkedIn (`OPERATOR_LINKEDIN`, footer), exempted by name in `check-dist.mjs`; `www.linkedin.com` is on its host allowlist for that link only.
- Anyone with a Trakt account can connect. Some Trakt features need VIP and Trakt's account caps apply; `/terms` ("Who can use it") says so. Update it if Trakt's policy changes.
- Edit `LEGAL_UPDATED` in `src/data/site.ts` whenever `/privacy` or `/terms` changes.
- The rate limits in `/terms` (120 reads, 30 writes per user per minute) must equal `RATE_LIMIT_READS` and `RATE_LIMIT_WRITES` on the deployed server; they are the server defaults.
- Build output is `build.format: 'file'`, `trailingSlash: 'never'`: the host must serve `/privacy` from `privacy.html` and `/terms` from `terms.html`. `python3 -m http.server` does not; use the `.html` URLs locally.

## Status

- Hosting not chosen. Deploy only once the server answers on the `mcp.` host, so the connector URL in the hero works.
- The Go server renders no page: `GET /` redirects to `--site-url`, which must be this site's origin (spec section 5.1).
- Hostnames are placeholders until the public hostname is confirmed (spec section 17): change `SITE_URL` and `MCP_URL` in `src/data/site.ts`, then `public/robots.txt` (`Sitemap:` line).
- Trakt's API use policy and branding rules are unconfirmed (spec section 17): recheck the copy and the footer disclaimer before launch.
