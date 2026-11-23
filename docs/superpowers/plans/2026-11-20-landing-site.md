# Landing Site Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Build `site/`, a static Astro website with a landing page, a privacy policy and terms of service, for the hosted google-contacts-mcp.

**Architecture:** Fully static Astro site, plain CSS driven by design tokens, no UI framework. One small inline script handles the copy button and the hero card animation. A Node script checks the built `dist/` against the spec, and runs as the site's main gate.

**Tech Stack:** Astro 7.4.13, `@astrojs/sitemap` 3.7.6, TypeScript 7.0.2, `@astrojs/check` 0.9.12 (re-check with `npm view <pkg> version` at install and use the newest stable), Fontsource fonts, `sharp` for the one-off OG image, Node 26 built-in test runner.

**Spec:** `docs/superpowers/specs/2026-11-20-landing-site-design.md`. Read it first: it holds the concept, tokens, wireframe and page content.

## Global Constraints

- Work in a git worktree under `.claude/worktrees/`, branch `feat/landing-site` (no "claude" or "worktree" in the branch name). Fetch `main` first.
- Commit only when the owner asks. Each task's commit step gives the message to use then. Conventional Commits, no mention of Claude. Author Samuel Berthe, repository `samber/*` identity.
- Site URL `https://google-contacts-mcp.samber.dev`; MCP URL `https://mcp.google-contacts-mcp.samber.dev/mcp`; repository `https://github.com/samber/google-contacts-mcp`; operator `Samuel Berthe`; public e-mail `hello@samuel-berthe.fr`.
- Plain CSS only: no Tailwind, no CSS framework, no UI framework. English only.
- No third-party request at page load: fonts are self-hosted, no analytics, no cookies, no tracker.
- `astro.config.mjs`: `output: 'static'`, `site` set to the site URL, `trailingSlash: 'never'`, `build.format: 'file'`. Canonical URLs are `/`, `/privacy`, `/terms`.
- Palette tokens, light then dark: `--desk` `#E4E9EE` / `#0F1720`; `--card` `#FFFFFF` / `#1B2631`; `--ink` `#16202A` / `#E8EEF4`; `--rule` `#B4CBE3` / `#34506B`; `--red` `#D33F49` / `#FF6B73`; `--mark` `#FFE066` / `#F2D14A`.
- Type: Schibsted Grotesk (variable) for headings and body, Courier Prime for text "written on the card". Body 17px, scale about 1.25, body line length under 70 characters.
- Accessibility floor: WCAG AA contrast in both themes, visible focus ring, one `h1` per page, `prefers-reduced-motion` respected, usable at 360px wide.
- Example data is fictional. No real person or company appears.
- The page never says the service is open to everyone, and carries no Testing-mode note.
- Google's Limited Use sentence stays verbatim in the privacy policy: "This service's use and transfer of information received from Google APIs adheres to the Google API Services User Data Policy, including the Limited Use requirements." (with the policy link).
- The Go module and `internal/` are not changed by this plan, except the one README line in Task 8.

## Review Focus

- Clipboard API absent or rejected (insecure context, denied permission): the URL stays selectable and the button says so; nothing throws. Test in Task 4.
- JavaScript disabled: the hero card shows its final content and the URL is readable; the copy button is not shown dead. Test in Task 4.
- `prefers-reduced-motion: reduce`: no animation, final card state at once. Test in Task 4.
- Narrow screens (360px): the long connector URL wraps instead of overflowing, and the A to Z strip does not push the layout sideways. Visual check in Task 4.
- Facts drifting from the code: the tool list, the stored and never-stored lists, and the rate limits on the site must match `internal/` and `README.md`. Check in Tasks 5 and 6.

---

## File map

```
site/
  package.json  astro.config.mjs  tsconfig.json  .gitignore  CLAUDE.md
  public/        favicon.svg  robots.txt  llms.txt  og.png
  scripts/       check-dist.mjs  check-contrast.mjs  og.mjs
  src/data/      site.ts        constants, tools list, stored lists, legal dates
  src/styles/    tokens.css  global.css
  src/scripts/   copy.ts  copy.test.ts  reveal.ts
  src/layouts/   Base.astro  Legal.astro
  src/components/ Header.astro Footer.astro IndexCard.astro AlphaStrip.astro
                 HeroCard.astro ConnectorUrl.astro ToolsCard.astro
                 SignatureDemo.astro StoredCards.astro OpenSource.astro
  src/pages/     index.astro privacy.astro terms.astro 404.astro
```

### Task 1: Scaffold the Astro project

**Files:**
- Create: `site/package.json`, `site/astro.config.mjs`, `site/tsconfig.json`, `site/.gitignore`, `site/src/pages/index.astro` (placeholder `h1`), `site/src/data/site.ts`

**Interfaces:**
- Produces: `src/data/site.ts` exporting `SITE_URL`, `MCP_URL`, `REPO_URL`, `OPERATOR_NAME`, `OPERATOR_EMAIL` (all `string`), values from Global Constraints. npm scripts: `dev`, `build` (`astro build`), `check` (`astro check`), `verify` (`node scripts/check-contrast.mjs && node scripts/check-dist.mjs`), `test` (`node --test src/scripts/`).

- [ ] **Step 1:** In `site/`, run `npm init -y`, then `npm install astro @astrojs/sitemap @fontsource-variable/schibsted-grotesk @fontsource/courier-prime` and `npm install -D typescript @astrojs/check sharp`. Confirm installed versions are the newest stable (`npm ls --depth=0`). Add `.gitignore` entries `node_modules/`, `dist/`, `.astro/`.
- [ ] **Step 2:** Write `astro.config.mjs` with the constraints above and the sitemap integration; `tsconfig.json` extends `astro/tsconfigs/strict`.
- [ ] **Step 3:** Write `src/data/site.ts` and a placeholder `index.astro`.
- [ ] **Step 4:** Run `npx astro check && npx astro build`. Expected: no errors, `dist/index.html` exists.
- [ ] **Step 5: Commit** `feat(site): scaffold the Astro project`

### Task 2: The dist gate (test first)

**Files:**
- Create: `site/scripts/check-dist.mjs`

**Interfaces:**
- Consumes: `dist/` from `astro build`.
- Produces: `node scripts/check-dist.mjs` exits 0 only if every assertion holds, and prints each failure. Later tasks make it pass piece by piece.

- [ ] **Step 1:** Write the script with these assertions over `dist/*.html`: files `index.html`, `privacy.html`, `terms.html`, `404.html` exist; each has `<html lang="en">`, a `<title>`, a meta description, exactly one `<h1>`, and `<link rel="canonical">` equal to `SITE_URL + path` (`/` for index, no trailing slash elsewhere); `index.html` contains `MCP_URL`; every page links `/privacy`, `/terms` and `REPO_URL` in the footer; no `href`, `src` or CSS `url()` points to a host other than `google-contacts-mcp.samber.dev`, `github.com`, `developers.google.com`, `myaccount.google.com`, `modelcontextprotocol.io`; `sitemap-index.xml`, `robots.txt` (mentioning the sitemap) and `llms.txt` (linking `/privacy`, `/terms` and the repository) exist; the home page has a JSON-LD block parsing as JSON with `@type` `SoftwareApplication`; `og.png` exists and is referenced by `og:image`; the total gzipped size of inline `<script>` content in `index.html` is under 1024 bytes.
- [ ] **Step 2:** Run `npm run build && node scripts/check-dist.mjs`. Expected: FAIL, listing the missing pages and metadata.
- [ ] **Step 3: Commit** `test(site): add the dist gate`

### Task 3: Design tokens, base layout, header and footer

**Files:**
- Create: `src/styles/tokens.css`, `src/styles/global.css`, `src/layouts/Base.astro`, `src/components/Header.astro`, `src/components/Footer.astro`, `scripts/check-contrast.mjs`
- Modify: `src/pages/index.astro`

**Interfaces:**
- Produces: `Base.astro` props `{ title: string; description: string; path: string; ogImage?: string }` and a default slot plus a named `head` slot; it renders canonical, Open Graph, Twitter tags, the header and footer. `Header.astro` and `Footer.astro` take no props. `check-contrast.mjs` parses the six tokens from `tokens.css` and fails when any pair below drops under its ratio.

- [ ] **Step 1:** Write `check-contrast.mjs`: ratios (WCAG relative luminance) of `--ink` on `--desk` and on `--card` at least 7, `--red` on `--card` at least 4.5, `--ink` on `--mark` at least 7, in both light and dark blocks. Run it. Expected: FAIL (no `tokens.css`).
- [ ] **Step 2:** Write `tokens.css`: light tokens on `:root`, dark tokens under `@media (prefers-color-scheme: dark)`, plus spacing and type-scale custom properties. Re-run: PASS. If `--red` fails, darken it and report the value.
- [ ] **Step 3:** Write `global.css`: Fontsource imports, base typography (Schibsted Grotesk body, Courier Prime via a `.typed` class), `:focus-visible` ring in `--red`, `prefers-reduced-motion` reset, `overflow-wrap: anywhere` on the `.typed` class.
- [ ] **Step 4:** Write `Base.astro`, `Header.astro` (brand, links `Connect` to `/#connect`, `Privacy`, `Terms`, `GitHub`), `Footer.astro` (operator, e-mail, links `/privacy`, `/terms`, GitHub). Use the layout in `index.astro`.
- [ ] **Step 5:** Run `npm run build && node scripts/check-dist.mjs`. Expected: failures remain only for pages and assets not built yet.
- [ ] **Step 6: Commit** `feat(site): add design tokens, base layout, header and footer`

### Task 4: Hero: the index card and the connector URL

**Files:**
- Create: `src/components/IndexCard.astro`, `src/components/AlphaStrip.astro`, `src/components/HeroCard.astro`, `src/components/ConnectorUrl.astro`, `src/scripts/copy.ts`, `src/scripts/copy.test.ts`, `src/scripts/reveal.ts`
- Modify: `src/pages/index.astro`

**Interfaces:**
- Produces: `IndexCard.astro` (slot, optional `header: string` for the red rule). `AlphaStrip.astro` props `{ active: string }` (one letter). `HeroCard.astro` renders the fictional card with final text in the HTML. `copy.ts` exports `copyText(text: string, clipboard: Pick<Clipboard, 'writeText'> | undefined): Promise<'copied' | 'unavailable'>`, never throws. `reveal.ts` exports `startReveal(root: HTMLElement, reduceMotion: boolean): void`.

- [ ] **Step 1:** Write `copy.test.ts` with `node:test`: a clipboard stub that resolves gives `'copied'`; a stub that rejects gives `'unavailable'`; `undefined` gives `'unavailable'`. Run `npm test`. Expected: FAIL.
- [ ] **Step 2:** Implement `copy.ts`. Run `npm test`. Expected: PASS.
- [ ] **Step 3:** Build the hero in `index.astro`: `h1` "Your assistant keeps your address book up to date.", a sub line "Claude and ChatGPT search, create and enrich your Google Contacts.", `ConnectorUrl` (id `connect`) showing `MCP_URL` in `.typed` selectable text and a copy button with `hidden` removed by script, an `aria-live="polite"` status ("Copied" or "Select the URL to copy it"), and two lines "Claude: Settings, Connectors, Add custom connector." and "ChatGPT: developer mode, Create app." The hero card reads `Durand, Camille`, `Acme`, `camille@acme.example`, a LinkedIn site line, filed under letter D in `AlphaStrip`.
- [ ] **Step 4:** `reveal.ts` hides the card fields, then reveals them one by one in about 2.5 seconds total, then marks the active letter in `--mark`. It does nothing visible when `reduceMotion` is true. Inline the module in `index.astro`, so no extra request.
- [ ] **Step 5:** Run `npm test && npm run check && npm run build && node scripts/check-dist.mjs`. Expected: the script-size and connector-URL assertions pass.
- [ ] **Step 6:** Visual check: serve `dist/` with `python3 -m http.server -d dist 4321` and look at it in Claude in Chrome, light and dark (emulate with the OS theme), then at 360px wide by loading it in a same-origin `<iframe width="360">` on a scratch page. Confirm the URL wraps, the strip does not overflow, the card writes itself once, and reduced motion shows the final card. Fix what looks wrong.
- [ ] **Step 7: Commit** `feat(site): add the hero with the animated index card and copy button`

### Task 5: The rest of the landing page

**Files:**
- Create: `src/components/ToolsCard.astro`, `SignatureDemo.astro`, `StoredCards.astro`, `OpenSource.astro`
- Modify: `src/pages/index.astro`, `src/data/site.ts`, `scripts/check-dist.mjs`

**Interfaces:**
- Produces in `site.ts`: `TOOLS: readonly { name: string; summary: string }[]` (ten entries), `STORED: readonly string[]`, `NEVER_STORED: readonly string[]`.

- [ ] **Step 1:** Add to `check-dist.mjs`: every `TOOLS` name appears in `index.html`; and the tool names found in `../internal/tools/*.go` (read the registration code first and adapt the pattern) equal the `TOOLS` names. Run the gate. Expected: FAIL.
- [ ] **Step 2:** Fill `TOOLS`, `STORED` and `NEVER_STORED` from `README.md` (the tool table and the "What is stored" table), checked against the code. Plain wording, no marketing. Include the rate limits only if shown (120 reads and 30 writes per user per minute, from `internal/config`).
- [ ] **Step 3:** Build the four sections per the spec: a ruled card listing the tools, two side-by-side cards "signature to contact" with a fictional signature, a stored and never-stored pair linking `/privacy`, and the open-source section linking `REPO_URL`. Vary card size and role; the only corner radius is the card's.
- [ ] **Step 4:** Run the gate again and the visual check of Task 4 Step 6. Expected: PASS for the new assertions.
- [ ] **Step 5: Commit** `feat(site): add tools, signature demo, data and open-source sections`

### Task 6: Privacy policy and terms of service

**Files:**
- Create: `src/layouts/Legal.astro`, `src/pages/privacy.astro`, `src/pages/terms.astro`
- Modify: `src/data/site.ts` (add `LEGAL_UPDATED = '2026-11-20'`), `scripts/check-dist.mjs`

**Interfaces:**
- Produces: `Legal.astro` props `{ title: string; description: string; path: string; updated: string }` and a default slot, rendering a long-document card at 65 characters per line.

- [ ] **Step 1:** Add to the gate: `privacy.html` and `terms.html` contain `OPERATOR_EMAIL`, the date `LEGAL_UPDATED`, a link to each other, and the exact Limited Use sentence and Google policy link (privacy only); `privacy.html` contains the words "cookies" and "analytics" in a sentence saying none are used; `terms.html` contains "French law". Run. Expected: FAIL.
- [ ] **Step 2:** Port the policy from `internal/web/templates/privacy.html` and the README "What is stored" section. Change only what the new setup requires: persistent volume and possible host backups (current wording in the template), the website sets no cookies and runs no analytics, operator contact is `OPERATOR_EMAIL`. Verify every claim against `internal/` and `README.md`; report any mismatch to the owner instead of choosing.
- [ ] **Step 3:** Write the terms with the sections listed in the spec (provider, who may use it, what the user authorizes, acceptable use with the 120 and 30 per-minute limits, as-is service and no guarantees, suspension and ending including the 180-day purge, liability, changes, French law with no court named, contact). Plain language, drafted from observed behavior. Add a visible note that the owner should review it with a lawyer if needed.
- [ ] **Step 4:** Run the gate. Expected: PASS for legal pages.
- [ ] **Step 5: Commit** `feat(site): add the privacy policy and the terms of service`

### Task 7: Search and AI discovery assets, 404, favicon

**Files:**
- Create: `src/pages/404.astro`, `public/favicon.svg`, `public/robots.txt`, `public/llms.txt`, `scripts/og.mjs`, `public/og.png`
- Modify: `src/layouts/Base.astro` (OG and favicon tags), `src/pages/index.astro` (JSON-LD)

- [ ] **Step 1:** `404.astro`: one card, one sentence, a link home. Favicon: an SVG index card with a red top rule.
- [ ] **Step 2:** `robots.txt` allows all and points to `https://google-contacts-mcp.samber.dev/sitemap-index.xml`. `llms.txt` follows the llms.txt convention: product summary, then links to `/privacy`, `/terms`, the repository, and the MCP URL described as the connector endpoint.
- [ ] **Step 3:** `og.mjs` renders a 1200x630 PNG from an inline SVG of the hero card with `sharp` and writes `public/og.png`. Run it once and commit the PNG; the script is not part of the build.
- [ ] **Step 4:** Add the `SoftwareApplication` JSON-LD to the home page: name, description, `applicationCategory` `BusinessApplication`, `operatingSystem` `Web`, `url`, `author` (the operator), `isAccessibleForFree` true, no `offers` claim of availability. Wire `og:image` to `/og.png`.
- [ ] **Step 5:** Run `npm run build && node scripts/check-dist.mjs`. Expected: PASS, no failures.
- [ ] **Step 6: Commit** `feat(site): add SEO and AI discovery assets, 404 and favicon`

### Task 8: Docs, gates and final verification

**Files:**
- Create: `site/CLAUDE.md`
- Modify: `CLAUDE.md` (root), `README.md:102`

- [ ] **Step 1:** `site/CLAUDE.md`: purpose, the commands (`npm run check`, `npm run build`, `npm test`, `npm run verify`), the gate list from the spec, the domain table, the rules (no tracking, no external requests, fictional data, facts match code), and the hosting status ("not chosen; deploy after the server answers on the `mcp.` host").
- [ ] **Step 2:** Root `CLAUDE.md`: add a package-map line for `site/` pointing to `site/CLAUDE.md`. Fix `README.md:102`: replace "Use a local disk, never a network filesystem." with wording that matches the new setup (a persistent volume is supported, the journal mode is DELETE, one process only), and re-run `npx prettier --check README.md CLAUDE.md`.
- [ ] **Step 3:** From `site/`: `npm run check && npm test && npm run build && npm run verify`. Expected: all pass.
- [ ] **Step 4:** Run Lighthouse on `dist/` served locally (PageSpeed in Claude in Chrome), `/` and `/privacy`. Target above 90 on all four scores. Record the numbers in the final report; fix regressions.
- [ ] **Step 5:** Run the Go gates untouched by this plan (`gofmt -l .`, `go vet ./...`, `golangci-lint run`, `go test -race ./...`) to confirm nothing outside `site/` changed.
- [ ] **Step 6: Commit** `docs(site): document the site and its gates`
