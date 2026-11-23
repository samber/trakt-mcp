# Landing site design

Date: 2026-11-20. Status: draft, waiting for approval.

## Intent

The hosted instance of google-contacts-mcp needs a real home. Today the Go server serves a bare `/` and `/privacy` on `google-contacts-mcp.samber.dev`. The goal is a beautiful static website that does one job: a visitor copies the connector URL and adds it in Claude or ChatGPT. The site also hosts the privacy policy, which Google's OAuth consent screen and verification need on a verified domain.

The server moves to its own host. The site takes the old host.

| Role | URL |
| --- | --- |
| Website (landing page, privacy policy) | `https://google-contacts-mcp.samber.dev` |
| MCP server and OAuth issuer | `https://mcp.google-contacts-mcp.samber.dev`, MCP endpoint at `/mcp` |

## Goals

- A visitor understands in five seconds what the product does, and copies the connector URL in one click.
- The page is memorable: one distinctive idea, executed well, with everything else quiet.
- The privacy policy is plain, accurate, and matches what the code does.
- Fast and light: static HTML, no framework runtime, near-zero JavaScript, no third-party requests.
- Good for search and AI assistants: correct metadata, `sitemap.xml`, `robots.txt`, `llms.txt`, structured data.

## Non-goals

- No blog, docs site, pricing, login, or form. Docs stay in the README.
- No analytics, cookies or trackers. The privacy policy says so, and the page must keep it true.
- No CSS framework (Tailwind or other), no UI framework (React, Vue, Svelte).
- No translations. English only.

## Anti-goals

- A generic SaaS template: gradient hero, identical rounded cards, three feature icons.
- Availability claims the owner has not confirmed.
- Any request to a third-party host at page load, such as Google Fonts or a CDN.

## Constraints

- Latest stable Astro and libraries, installed from npm at build time: Astro 7.4.13, `@astrojs/sitemap` 3.7.6, TypeScript 7.0.2, `@astrojs/check` 0.9.12 on 2026-11-20. Re-check before install. Node 26 is installed locally; Astro needs 22.23.4 or newer.
- Static output. No adapter. Hosting is not decided and any static host works.
- Plain CSS: global design tokens as custom properties, scoped styles in components.
- Fonts self-hosted through Fontsource packages, never fetched from Google.
- The site lives in `site/`, with its own `package.json` and `CLAUDE.md`. The Go module is untouched by this part.
- Public repo: no private information, no internal hostnames, no tracking.

## Approaches

1. **Astro static site, plain CSS, one small script.** Chosen. Best fit: content site, mostly static, one interactive moment (copy button and the card animation) that vanilla JS covers.
2. Static HTML files by hand. Less tooling, but the privacy page and the landing page share layout, metadata and tokens, and Astro gives layouts, a sitemap integration and type checking for free.
3. Astro with a UI framework island for the hero. Rejected: the animation is a timed text reveal, and a framework runtime would cost more than the whole page.

## Design

### Concept: the index card

The product manages an address book, so the page is a card file. One idea carries it: **a contact card that fills in field by field** while an assistant "works", filed under its letter.

The hero shows the headline, the connector URL with a copy button, and a single index card. On load the card writes itself once, in typewriter type: name, company, e-mail, a LinkedIn site. The matching letter tab on an A to Z strip lights up in highlighter yellow. Then it stops. That is the only autonomous motion on the page. With `prefers-reduced-motion`, the card shows its final state.

### Tokens

Palette, chosen to avoid the cream and terracotta and the black and acid-green defaults. It borrows from a real index card: white stock, blue ruled lines, a red header rule.

| Token | Light | Dark | Role |
| --- | --- | --- | --- |
| `--desk` | `#E4E9EE` | `#0F1720` | page background |
| `--card` | `#FFFFFF` | `#1B2631` | card stock |
| `--ink` | `#16202A` | `#E8EEF4` | text |
| `--rule` | `#B4CBE3` | `#34506B` | ruled lines, borders |
| `--red` | `#D33F49` | `#FF6B73` | the card's header rule, links on hover |
| `--mark` | `#FFE066` | `#F2D14A` | highlighter: active letter, copy confirmation |

Type, two clearly different families, both self-hosted:

- **Schibsted Grotesk** (variable) for headings and body. Characterful, not the usual default.
- **Courier Prime** for everything written "on the card": the connector URL, card fields, tool names.

Scale: a modular scale around 1.25 from a 17px body. Body line length stays under 70 characters. Headings set tight with a heavy weight, no italic or colored single word.

### Layout

Left-aligned text on a 12-column grid. A card stack on the right of the hero, the A to Z strip down its edge. Below the hero, each section is a card on the desk. Cards differ in size and role, with one corner radius only on the card (paper has small, even corners); the page itself has none.

```
+----------------------------------------------------------+
| Google Contacts MCP          Connect  Privacy  GitHub    |
|                                                       A  |
|  Your assistant keeps                                 B  |
|  your address book                                   [C] |
|  up to date.                  +-------------------+    D  |
|                               | Camille Durand    |    E  |
|  Claude and ChatGPT search,   | Acme, CTO         |       |
|  create and enrich your       | camille@acme.com  |       |
|  Google Contacts.             | linkedin.com/in/..|       |
|                               +-------------------+       |
|  [ https://mcp.google-contacts-mcp.samber.dev/mcp ][Copy] |
|  Claude: Settings > Connectors > Add custom connector     |
+----------------------------------------------------------+
```

### Pages and sections

`/`:

1. **Hero** as above. One call to action: copy the connector URL. Two lines under it: how to add it in Claude and in ChatGPT.
2. **What your assistant can do.** A ruled card listing the real tools in plain words: find, read, create, update, enrich by e-mail, delete, organize in labels. Names in typewriter type.
3. **From e-mail signature to contact.** Two cards side by side: a pasted signature, and the contact it becomes. This is the README's headline use case, shown as before and after. Content is static; no animation.
4. **What it keeps, what it never keeps.** Two short lists, from the README table. Link to the full policy.
5. **Open source.** One line, one link to GitHub: one Go binary, one SQLite file, self-hostable.
6. **Footer.** Operator name, links to the privacy policy, the terms of service and GitHub.

`/privacy`: the full policy, a card-styled long document, readable at 65 characters per line. Text comes from the current `privacy.html` and the README's "What is stored" section, updated for the new setup. Shows a "last updated" date.

`/terms`: the terms of service, same long-document layout as `/privacy`, linked from the header menu, the footer and the privacy policy. Plain-language sections, all drafted from how the service actually behaves:

- who provides the service (the operator, an individual) and what it is;
- who may use it: any Google account holder, unless the operator restricts the e-mail domains;
- what the user authorizes: the assistant acts on their contacts through the Google People API, and the user stays responsible for what they ask it to do, including bulk edits and deletes;
- acceptable use, including no abuse of the rate limits (120 reads and 30 writes per user per minute) and compliance with Google's terms and the assistant provider's terms;
- the service is provided "as is", with no uptime guarantee, no backup guarantee, and a note that contacts changed by an assistant can be lost or overwritten, so users should keep their own export;
- suspension and ending: by the user (revoke at Google or ask the operator) and by the operator (abuse, inactivity for 180 days, shutdown with notice on the site);
- liability, as far as the law of the governing country allows;
- changes to the terms, with a "last updated" date;
- governing law and contact.

The terms are a draft for the owner to read and amend, not legal advice. The page shows a "last updated" date.

`/404`: a card with one line and a link home.

### Interactivity

One inline module script, under 1 KB gzipped, no dependency:

- Copy button: writes the URL with `navigator.clipboard`, highlights the button in `--mark`, announces "Copied" in an `aria-live` region. If the clipboard API fails, the URL stays selectable text.
- Hero card: reveals fields in sequence once. Skipped under `prefers-reduced-motion`.

Everything else is static HTML and CSS.

### SEO and AI discovery

- Per-page `<title>`, description, canonical URL, Open Graph and Twitter card tags.
- `og.png`, 1200x630, generated once from an SVG with a script in `site/scripts/` and committed.
- JSON-LD `SoftwareApplication` on `/`.
- `sitemap-index.xml` through `@astrojs/sitemap`, `robots.txt`, and `llms.txt` describing the product and linking the privacy policy, the terms and the repository.
- Semantic landmarks, one `h1` per page, visible focus rings, WCAG AA contrast in both themes, reduced motion respected, usable at 360px wide.

### Quality gates for `site/`

Documented in `site/CLAUDE.md`, run before every commit that touches `site/`:

- `npx astro check`
- `npx astro build`
- A small Node script over `dist/` that fails when a page has no canonical URL, when any HTML references an external host other than the GitHub and Google links in the text, or when the connector URL is missing from the home page.
- Lighthouse or PageSpeed on the built pages in a browser, target above 90 on all four scores.

## Part 2: the Go server (separate work, after the site is live)

You chose: remove `/privacy` from the server, and redirect `/` to the landing page. This is a separate project with its own plan, and it comes second. Removing `/privacy` before the new site serves it would break the privacy URL registered with Google.

What it touches, to be confirmed by reading the code at planning time:

- `internal/web`: delete the privacy template and route; `GET /` redirects to a configured site URL.
- `internal/config`: a new `--site-url` flag (`GCMCP_SITE_URL`), kept in sync with the README table. My proposal: optional, and when empty the server keeps its minimal built-in home page, so self-hosters without a website still get a working `/`. Redirect status: 302, so a wrong setting is never cached for good.
- `--operator-name` and `--operator-email` are required today and shown on `/` and `/privacy`. `internal/oauth/provider.go` also uses the e-mail. They must stay required only where still used. Decide when planning.
- Tests, README, CLAUDE.md and the spec change with it.
- Moving the server to `mcp.google-contacts-mcp.samber.dev` changes the OAuth issuer and `GCMCP_BASE_URL`. This is configuration, not code, but it needs a checklist: the Google OAuth client's redirect URI, the consent screen's home and privacy links, the DNS and TLS for the new host, and every user re-adding the connector, since their tokens were issued by the old issuer.
- Hosted-instance configuration to set at deploy time: `GCMCP_RATE_LIMIT_READS=50` and `GCMCP_RATE_LIMIT_WRITES=20` (the terms state these limits; the server defaults are 120 and 30), and `GCMCP_LOG_LEVEL=info` (the privacy policy describes what is logged at that level).

## Owner decisions and open items

Decided by the owner:

- The public e-mail on the privacy policy, the terms and the footer is `hello@samuel-berthe.fr`.
- The terms are governed by French law, with no court named.
- The Google app is out of testing mode (owner, 2026-11-20 update). The hero carries no Testing-mode note, and the terms say anyone with a Google account can connect, unless the operator restricts the e-mail domains.

Still open:

- Whether the terms say the service is free, as is, with no service-level commitment. My proposal: yes, since nothing is sold today.
- Where `site/` is hosted. Not needed to build.

## Calls made while reviewing (the owner can overrule any of them)

- **Example data is fictional.** The hero card reads "Durand, Camille / Acme / camille@acme.example". Contacts are filed by surname, so the strip highlights D, not C. The signature example is invented too. No real person or company appears.
- **Source of truth for facts.** The Go code decides. The builder checks every claim on the tool list, on what is stored, and in the policy against `internal/` and `README.md`, and reports any mismatch instead of guessing. The privacy text changes only where the new setup requires it: the database sits on a persistent volume, backups may exist at the host, and the website itself sets no cookie and runs no analytics.
- **Order of work.** The site is built now. It is deployed once the server answers on `mcp.google-contacts-mcp.samber.dev`, so the connector URL shown in the hero exists. The site keeps `/privacy` at exactly the path the old server used, so the URL registered with Google stays valid.
- **Output and hosting.** Neutral static output with `trailingSlash: 'never'` and `build.format: 'file'`, so `/privacy` and `/terms` are the canonical URLs. Deploy automation and host-specific files (`_headers`, `_redirects`) are out of scope until a host is chosen.
- **Repository docs and CI.** The root `CLAUDE.md` gets a package-map line and a pointer to `site/CLAUDE.md`, which holds the site's gates. A CI job for the site is out of scope for now.
- **Lighthouse is a manual pre-launch check**, not a scripted gate, so the site stays free of heavy dev dependencies.

## Left to the builder

Exact copy within the content above, spacing and type scale values, animation timing, the structure of components, the OG image artwork, the `llms.txt` wording, and the smoke-test script.
