// Renders public/og.png (1200x630), the social preview image, from an inline SVG.
// Run by hand when the design changes: `node scripts/og.mjs`, then commit the PNG.
// It is not part of the build. Fonts are system fonts: librsvg cannot load the web fonts.
// Colours are the light palette of src/styles/tokens.css.
import { fileURLToPath } from 'node:url';
import sharp from 'sharp';

const OUT = fileURLToPath(new URL('../public/og.png', import.meta.url));

const WIDTH = 1200;
const HEIGHT = 630;
const SANS = 'Helvetica Neue, Helvetica, Arial, sans-serif';
const MONO = 'Menlo, Courier New, monospace';

const DESK = '#f4f5f6';
const CARD = '#ffffff';
const INK = '#15171a';
const MUTED = '#5b616b';
const RED = '#c8372d';
const MARK = '#fde4df';
const LINE = '#e2e4e8';

// Same fictional contact as the hero demo; `changed` is the row the assistant just wrote.
const FIELDS = [
  { label: 'Name', value: 'Camille Durand' },
  { label: 'Company', value: 'Halvard Robotics' },
  { label: 'Title', value: 'CTO', changed: true },
  { label: 'E-mail', value: 'camille@halvard.example', mono: true },
];

// Contact panel geometry.
const PANEL_X = 680;
const PANEL_W = 448;
const ROW_TOP = 268;
const ROW_H = 64;

const rows = FIELDS.map(({ label, value, changed, mono }, index) => {
  const y = ROW_TOP + index * ROW_H;
  const fill = changed ? `<rect x="${PANEL_X + 24}" y="${y}" width="${PANEL_W - 48}" height="${ROW_H}" fill="${MARK}"/>` : '';
  const rule = index > 0 ? `<rect x="${PANEL_X + 24}" y="${y}" width="${PANEL_W - 48}" height="1" fill="${LINE}"/>` : '';
  return `${fill}${rule}
    <text x="${PANEL_X + 44}" y="${y + 40}" font-family="${SANS}" font-size="20" fill="${changed ? INK : MUTED}">${label}</text>
    <text x="${PANEL_X + 150}" y="${y + 40}" font-family="${mono ? MONO : SANS}" font-size="${mono ? 17 : 22}" font-weight="500" fill="${INK}">${value}</text>`;
}).join('\n    ');

const svg = `<svg xmlns="http://www.w3.org/2000/svg" width="${WIDTH}" height="${HEIGHT}" viewBox="0 0 ${WIDTH} ${HEIGHT}">
  <rect width="${WIDTH}" height="${HEIGHT}" fill="${DESK}"/>

  <!-- public/favicon.svg (32x32 viewBox), scaled to 44px. -->
  <g transform="translate(72 72) scale(1.375)">
    <rect x="3" y="6" width="26" height="20" rx="2" fill="#ffffff" stroke="#b4cbe3" stroke-width="1.5"/>
    <rect x="3" y="11" width="26" height="2" fill="#d33f49"/>
    <rect x="7" y="17" width="14" height="1.6" fill="#b4cbe3"/>
    <rect x="7" y="21" width="10" height="1.6" fill="#b4cbe3"/>
  </g>
  <text x="132" y="103" font-family="${SANS}" font-size="26" font-weight="700" fill="${INK}">Google Contacts MCP</text>

  <g fill="${INK}" font-family="${SANS}" font-weight="800" letter-spacing="-1.5">
    <text x="72" y="250" font-size="60">Your assistant</text>
    <text x="72" y="322" font-size="60">keeps your address</text>
    <text x="72" y="394" font-size="60">book up to date.</text>
  </g>
  <g fill="${MUTED}" font-family="${SANS}" font-size="28">
    <text x="72" y="470">Claude and ChatGPT search, create</text>
    <text x="72" y="508">and enrich your Google Contacts.</text>
  </g>

  <rect x="${PANEL_X}" y="120" width="${PANEL_W}" height="430" rx="16" fill="${CARD}" stroke="${LINE}"/>
  <rect x="${PANEL_X + 24}" y="148" width="250" height="40" rx="8" fill="none" stroke="${LINE}"/>
  <text x="${PANEL_X + 40}" y="175" font-family="${MONO}" font-size="19" font-weight="700" fill="${RED}">update_contact</text>
  <text x="${PANEL_X + 24}" y="236" font-family="${SANS}" font-size="22" fill="${INK}">Her card is updated:</text>
  ${rows}
</svg>`;

await sharp(Buffer.from(svg)).png().toFile(OUT);
// bearer:disable javascript_lang_logger_leak
console.log(`wrote ${OUT}`);
