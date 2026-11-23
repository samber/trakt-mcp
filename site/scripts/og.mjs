// Renders public/og.png (1200x630), the social preview image: the logo mark, the wordmark and a tagline.
// Run by hand when the logo or tagline changes: `node scripts/og.mjs`, then commit the PNG.
// It is not part of the build. Fonts are system fonts: librsvg cannot load the web fonts.
import { readFileSync } from 'node:fs';
import { fileURLToPath } from 'node:url';
import sharp from 'sharp';

const MARK_SVG = fileURLToPath(new URL('../public/favicon.svg', import.meta.url));
const OUT = fileURLToPath(new URL('../public/og.png', import.meta.url));

const WIDTH = 1200;
const HEIGHT = 630;
const SANS = 'Helvetica Neue, Helvetica, Arial, sans-serif';

// Logo palette: navy ground of the mark for the wordmark, a neutral grey for secondary text.
const DESK = '#f4f5f6';
const INK = '#1d2b53';
const MUTED = '#5b616b';

// The mark is drawn on a 32-unit grid; 7x gives a 224 px mark.
const MARK_UNITS = 32;
const MARK_SCALE = 7;
const MARK_X = 112;
const MARK_Y = (HEIGHT - MARK_UNITS * MARK_SCALE) / 2;
const TEXT_X = MARK_X + MARK_UNITS * MARK_SCALE + 56;

// Inner markup of favicon.svg, so the preview always shows the shipped mark.
const mark = readFileSync(MARK_SVG, 'utf8')
  .replace(/^\s*<svg[^>]*>/, '')
  .replace(/<\/svg>\s*$/, '');

const svg = `<svg xmlns="http://www.w3.org/2000/svg" width="${WIDTH}" height="${HEIGHT}" viewBox="0 0 ${WIDTH} ${HEIGHT}">
  <rect width="${WIDTH}" height="${HEIGHT}" fill="${DESK}"/>
  <g transform="translate(${MARK_X} ${MARK_Y}) scale(${MARK_SCALE})">${mark}</g>
  <text x="${TEXT_X}" y="300" font-family="${SANS}" font-size="104" font-weight="800" letter-spacing="-3" fill="${INK}">Trakt MCP</text>
  <text x="${TEXT_X}" y="372" font-family="${SANS}" font-size="36" fill="${MUTED}">Your watchlist, in Claude and ChatGPT</text>
  <text x="${TEXT_X}" y="560" font-family="${SANS}" font-size="22" fill="${MUTED}">Unofficial. Not affiliated with Trakt.</text>
</svg>`;

await sharp(Buffer.from(svg)).png().toFile(OUT);
// bearer:disable javascript_lang_logger_leak
console.log(`wrote ${OUT}`);
