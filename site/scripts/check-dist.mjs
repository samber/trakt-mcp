// Gate over the built site: run `npm run build` first, then `node scripts/check-dist.mjs`.
// Exits 1 and prints every failure when the build breaks a rule of the design spec.
import { existsSync, readFileSync, readdirSync } from 'node:fs';
import { fileURLToPath } from 'node:url';
import { gzipSync } from 'node:zlib';

const {
  SITE_URL,
  MCP_URL,
  REPO_URL,
  OPERATOR_EMAIL,
  OPERATOR_LINKEDIN = '',
  LEGAL_UPDATED = '',
  TOOLS = [],
  STORED = [],
  NEVER_STORED = [],
} = await import('../src/data/site.ts');

// Go sources registering the MCP tools; the site's tool list must match them.
// DIST_DIR and GO_TOOLS_DIR exist so scripts/check-dist.test.mjs can point the gate at a broken copy.
const withSlash = (path) => (path.endsWith('/') ? path : `${path}/`);
const GO_TOOLS_DIR = withSlash(process.env.GO_TOOLS_DIR ?? fileURLToPath(new URL('../../internal/tools/', import.meta.url)));
const GO_TOOL_NAME = /Name:\s+"([a-z_]+)"/g;

const DIST = withSlash(process.env.DIST_DIR ?? fileURLToPath(new URL('../dist/', import.meta.url)));

// Pages the build must produce, with their canonical path. The 404 page is
// not canonical anywhere: it must be marked noindex instead.
const PAGES = [
  { file: 'index.html', path: '/' },
  { file: 'privacy.html', path: '/privacy' },
  { file: 'terms.html', path: '/terms' },
  { file: '404.html', path: null },
];

// Hosts the pages may link to. Anything else is a third-party request or an
// unreviewed outbound link.
const ALLOWED_HOSTS = new Set([
  new URL(SITE_URL).host,
  'github.com',
  // The operator's LinkedIn profile, linked from the footer (outbound link only).
  'www.linkedin.com',
  // Trakt's connected-apps page, linked from the privacy policy so users can revoke the app.
  'trakt.tv',
  'modelcontextprotocol.io',
  // The French data protection authority, named in the privacy policy.
  'www.cnil.fr',
  // Google Tag Manager, loaded on every page at the owner's request.
  'www.googletagmanager.com',
  // PostHog through the owner's first-party proxy, loaded on every page at the owner's request.
  'hogpost3.samber.dev',
  // Ahrefs Web Analytics, loaded on every page at the owner's request.
  'analytics.ahrefs.com',
]);

// The site is rewritten from the Google Contacts version: these strings must never come back
// in visible text, titles or the discovery files.
const FORBIDDEN_LEGACY = /google|contacts/i;
const PRODUCT_NAME = 'Trakt MCP';
// Branding rule (spec section 11): the name Trakt is descriptive only, never an endorsement claim.
const NOT_AFFILIATED_SENTENCE = 'not affiliated with or endorsed by Trakt';
// Spec section 11: deletion after a revoke at Trakt is not immediate.
const DELETION_DELAY_SENTENCE = 'deletion can take up to 180 days';
const TRAKT_CONNECTED_APPS_URL = 'https://trakt.tv/oauth/authorized_applications';

// Spec: the page script (copy button and card reveal) stays under 1 KB gzipped. The inline
// Google Tag Manager and PostHog loaders add about 0.3 KB more, so the cap is 1.5 KB.
const MAX_SCRIPT_GZIP_BYTES = 1536;

const failures = [];
const fail = (message) => failures.push(message);

const read = (name) => readFileSync(`${DIST}${name}`, 'utf8');

/**
 * Hosts of every external URL in HTML or CSS text: attribute values (including each
 * entry of a srcset), CSS url(), and CSS @import. A protocol-relative URL such as
 * "//cdn.example/x.css" counts as external.
 */
function* externalHosts(text) {
  const candidates = [];
  for (const [, value] of text.matchAll(/\s(?:href|src|action|poster|data|srcset)="([^"]*)"/g)) {
    for (const entry of value.split(',')) candidates.push(entry.trim().split(/\s+/)[0]);
  }
  for (const [, url] of text.matchAll(/url\(\s*["']?([^)"']+)/g)) candidates.push(url);
  for (const [, url] of text.matchAll(/@import\s+["']([^"']+)/g)) candidates.push(url);
  for (const url of candidates) {
    if (/^(https?:)?\/\//.test(url)) yield new URL(url.startsWith('//') ? `https:${url}` : url).host;
  }
}
const countMatches = (text, pattern) => (text.match(pattern) ?? []).length;

if (!existsSync(DIST)) {
  console.error('dist/ is missing: run `npm run build` first.');
  process.exit(1);
}

const jsonLdPattern = /<script[^>]*type="application\/ld\+json"[^>]*>([\s\S]*?)<\/script>/i;

function checkPage({ file, path }) {
  if (!existsSync(`${DIST}${file}`)) {
    fail(`${file}: page was not built`);
    return;
  }
  const html = read(file);
  if (!/<html[^>]*\slang="en"/.test(html)) fail(`${file}: <html lang="en"> is missing`);
  if (!/<title>[^<]+<\/title>/.test(html)) fail(`${file}: <title> is missing or empty`);
  if (!/<meta\s+name="description"\s+content="[^"]+"/.test(html)) fail(`${file}: meta description is missing`);
  if (countMatches(html, /<h1[\s>]/g) !== 1) fail(`${file}: expected exactly one <h1>`);

  if (path === null) {
    if (!/<meta\s+name="robots"\s+content="[^"]*noindex/.test(html)) fail(`${file}: must be noindex`);
  } else {
    const want = new URL(path, SITE_URL).href;
    const canonical = html.match(/<link\s+rel="canonical"\s+href="([^"]+)"/)?.[1];
    if (canonical !== want) fail(`${file}: canonical is ${canonical ?? 'missing'}, want ${want}`);
  }

  for (const href of ['/privacy', '/terms', REPO_URL]) {
    if (!html.includes(`href="${href}"`)) fail(`${file}: footer link to ${href} is missing`);
  }

  for (const host of externalHosts(html)) {
    if (!ALLOWED_HOSTS.has(host)) fail(`${file}: reference to disallowed host ${host}`);
  }
}

PAGES.forEach(checkPage);

// External references inside built CSS (for example a font URL).
const assetsDir = `${DIST}_astro/`;
if (existsSync(assetsDir)) {
  for (const name of readdirSync(assetsDir).filter((n) => n.endsWith('.css'))) {
    const css = readFileSync(`${assetsDir}${name}`, 'utf8');
    for (const host of externalHosts(css)) {
      if (!ALLOWED_HOSTS.has(host)) fail(`_astro/${name}: reference to disallowed host ${host}`);
    }
  }
}

// Home page rules.
if (existsSync(`${DIST}index.html`)) {
  const home = read('index.html');
  if (!home.includes(MCP_URL)) fail(`index.html: connector URL ${MCP_URL} is missing`);

  const jsonLd = home.match(jsonLdPattern)?.[1];
  if (!jsonLd) {
    fail('index.html: JSON-LD block is missing');
  } else {
    try {
      // The home page carries one @graph; each node type below must be in it.
      const types = (JSON.parse(jsonLd)['@graph'] ?? []).map((node) => node['@type']);
      for (const type of ['SoftwareApplication', 'WebSite', 'Person', 'FAQPage']) {
        if (!types.includes(type)) fail(`index.html: JSON-LD @graph has no ${type}`);
      }
    } catch {
      fail('index.html: JSON-LD is not valid JSON');
    }
  }

  const ogImage = home.match(/<meta\s+property="og:image"\s+content="([^"]+)"/)?.[1];
  if (!ogImage) {
    fail('index.html: og:image is missing');
  } else if (!existsSync(`${DIST}${new URL(ogImage).pathname.slice(1)}`)) {
    fail(`index.html: og:image ${ogImage} does not exist in dist/`);
  }

  // Page scripts: inline ones plus local ones, JSON-LD excluded.
  let scriptBytes = 0;
  for (const [, attrs, body] of home.matchAll(/<script([^>]*)>([\s\S]*?)<\/script>/g)) {
    if (/application\/ld\+json/.test(attrs)) continue;
    const src = attrs.match(/\ssrc="([^"]+)"/)?.[1];
    const code = src && src.startsWith('/') ? readFileSync(`${DIST}${src.slice(1)}`, 'utf8') : body;
    scriptBytes += gzipSync(code).length;
  }
  if (scriptBytes > MAX_SCRIPT_GZIP_BYTES) fail(`index.html: page scripts are ${scriptBytes} B gzipped, limit ${MAX_SCRIPT_GZIP_BYTES} B`);
}

// Facts shown on the home page must match the code.
if (TOOLS.length === 0) fail('site.ts: TOOLS is empty');
if (STORED.length === 0 || NEVER_STORED.length === 0) fail('site.ts: STORED or NEVER_STORED is empty');
if (existsSync(`${DIST}index.html`)) {
  const home = read('index.html');
  for (const { name } of TOOLS) {
    if (!home.includes(name)) fail(`index.html: tool ${name} is not listed`);
  }
  for (const item of [...STORED, ...NEVER_STORED]) {
    if (!home.includes(item)) fail(`index.html: stored/never-stored item is missing: ${item}`);
  }
}
if (!existsSync(GO_TOOLS_DIR)) {
  // Never skip silently: without the Go sources the tool list could drift unnoticed.
  fail(`internal/tools not found at ${GO_TOOLS_DIR}: cannot compare the tool list with the code`);
} else {
  const goNames = new Set();
  for (const name of readdirSync(GO_TOOLS_DIR).filter((n) => n.endsWith('.go') && !n.endsWith('_test.go'))) {
    for (const [, tool] of readFileSync(`${GO_TOOLS_DIR}${name}`, 'utf8').matchAll(GO_TOOL_NAME)) goNames.add(tool);
  }
  if (goNames.size === 0) fail('internal/tools: no tool registration found; the pattern in check-dist.mjs may be stale');
  const siteNames = new Set(TOOLS.map((t) => t.name));
  for (const name of goNames) if (!siteNames.has(name)) fail(`site.ts: TOOLS lacks ${name}, registered in internal/tools`);
  for (const name of siteNames) if (!goNames.has(name)) fail(`site.ts: tool ${name} is not registered in internal/tools`);
}

// Legal pages.
const visibleText = (html) =>
  html
    .replace(/<(script|style)[\s\S]*?<\/\1>/g, '')
    .replace(/<[^>]+>/g, '')
    .replace(/&#39;|&apos;/g, "'")
    .replace(/&amp;/g, '&')
    .replace(/\s+/g, ' ');
const mainOf = (html) => html.match(/<main[\s\S]*<\/main>/)?.[0] ?? '';

if (!LEGAL_UPDATED) fail('site.ts: LEGAL_UPDATED is missing');
for (const [file, other] of [
  ['privacy.html', '/terms'],
  ['terms.html', '/privacy'],
]) {
  if (!existsSync(`${DIST}${file}`)) continue;
  const html = read(file);
  if (!html.includes(OPERATOR_EMAIL)) fail(`${file}: operator e-mail is missing`);
  if (LEGAL_UPDATED && !html.includes(LEGAL_UPDATED)) fail(`${file}: last-updated date ${LEGAL_UPDATED} is missing`);
  if (!mainOf(html).includes(`href="${other}"`)) fail(`${file}: the page body does not link ${other}`);
}
if (existsSync(`${DIST}privacy.html`)) {
  const html = read('privacy.html');
  if (!visibleText(html).includes(DELETION_DELAY_SENTENCE)) fail(`privacy.html: the sentence "${DELETION_DELAY_SENTENCE}" is missing`);
  if (!html.includes(`href="${TRAKT_CONNECTED_APPS_URL}"`)) fail('privacy.html: link to the Trakt connected apps page is missing');
  if (!visibleText(html).includes('This website sets no cookies and runs no analytics.')) {
    fail('privacy.html: the no-cookies, no-analytics statement is missing');
  }
}
if (existsSync(`${DIST}terms.html`) && !visibleText(read('terms.html')).includes('French law')) {
  fail('terms.html: governing law (French law) is missing');
}

// Privacy policy statements the owner relies on.
if (existsSync(`${DIST}privacy.html`)) {
  const privacy = visibleText(read('privacy.html'));
  for (const phrase of [
    'returned to the assistant you connected',
    'arguments and results are not logged',
    'hosting provider',
    'portable',
    'data source and processor',
    'Connected Apps',
    'watch history',
    NOT_AFFILIATED_SENTENCE,
  ]) {
    if (!privacy.includes(phrase)) fail(`privacy.html: missing the statement "${phrase}"`);
  }
}

// Branding and legacy-string rules, over every built page and discovery file.
for (const { file } of PAGES) {
  if (!existsSync(`${DIST}${file}`)) continue;
  const html = read(file);
  const title = html.match(/<title>([^<]*)<\/title>/)?.[1] ?? '';
  if (!file.startsWith('404') && !title.includes('Trakt')) fail(`${file}: <title> does not name Trakt`);
  const legacy = visibleText(html).match(FORBIDDEN_LEGACY)?.[0];
  if (legacy) fail(`${file}: visible text still contains the legacy word "${legacy}"`);
  const meta = [...html.matchAll(/<meta[^>]+content="([^"]*)"/g)].map((m) => m[1]).join(' ');
  const metaLegacy = meta.match(FORBIDDEN_LEGACY)?.[0];
  if (metaLegacy) fail(`${file}: meta tags still contain the legacy word "${metaLegacy}"`);
}
if (existsSync(`${DIST}index.html`)) {
  const home = read('index.html');
  if (!/<title>[^<]*Trakt MCP/.test(home)) fail(`index.html: <title> must contain "${PRODUCT_NAME}"`);
  if (!visibleText(home).includes(NOT_AFFILIATED_SENTENCE)) fail(`index.html: the sentence "${NOT_AFFILIATED_SENTENCE}" is missing`);
  if (/trakt[^"]*\.(svg|png)/i.test(home.replace(/og\.png/g, ''))) fail('index.html: references an image named after Trakt (the logo must not be used)');
}
// robots.txt is not checked: it names the Google-Extended crawler.
if (existsSync(`${DIST}llms.txt`)) {
  const legacy = read('llms.txt').match(FORBIDDEN_LEGACY)?.[0];
  if (legacy) fail(`llms.txt: still contains the legacy word "${legacy}"`);
}
if (existsSync(`${DIST}manifest.json`)) {
  const legacy = read('manifest.json').match(FORBIDDEN_LEGACY)?.[0];
  if (legacy) fail(`manifest.json: still contains the legacy word "${legacy}"`);
  if (!read('manifest.json').includes('Trakt')) fail('manifest.json: does not name Trakt');
}

// Example data must be obviously fictional: a LinkedIn handle that could be a real person is not allowed.
// The one exception is the operator's own profile (OPERATOR_LINKEDIN), linked from the footer.
const operatorHandle = OPERATOR_LINKEDIN.match(/linkedin\.com\/in\/([A-Za-z0-9_-]+)/)?.[1];
for (const { file } of PAGES) {
  if (!existsSync(`${DIST}${file}`)) continue;
  for (const [, handle] of read(file).matchAll(/linkedin\.com\/in\/([A-Za-z0-9_-]+)/g)) {
    if (handle === operatorHandle) continue;
    if (!handle.includes('example')) fail(`${file}: LinkedIn handle "${handle}" does not look fictional (needs "example")`);
  }
}

// Crawler and assistant discovery files.
if (!existsSync(`${DIST}sitemap-index.xml`)) fail('sitemap-index.xml is missing');
if (!existsSync(`${DIST}robots.txt`)) {
  fail('robots.txt is missing');
} else if (!read('robots.txt').includes(`Sitemap: ${SITE_URL}/sitemap-index.xml`)) {
  fail('robots.txt does not point to the sitemap');
}
if (!existsSync(`${DIST}llms.txt`)) {
  fail('llms.txt is missing');
} else {
  const llms = read('llms.txt');
  for (const link of [`${SITE_URL}/privacy`, `${SITE_URL}/terms`, REPO_URL]) {
    if (!llms.includes(link)) fail(`llms.txt does not link ${link}`);
  }
}
if (!existsSync(`${DIST}og.png`)) fail('og.png is missing');

if (failures.length > 0) {
  // bearer:disable javascript_lang_logger_leak
  console.error(`${failures.length} check(s) failed:`);
  // bearer:disable javascript_lang_logger_leak
  for (const message of failures) console.error(`  - ${message}`);
  process.exit(1);
}
console.log('dist checks passed');
