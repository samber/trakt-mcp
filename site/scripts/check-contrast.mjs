// Fails when a text/background token pair of src/styles/tokens.css drops under
// its WCAG contrast ratio, in the light or the dark palette.
import { readFileSync } from 'node:fs';
import { fileURLToPath } from 'node:url';

const TOKENS_FILE = fileURLToPath(new URL('../src/styles/tokens.css', import.meta.url));

// Marks where the dark palette starts in tokens.css.
const DARK_MARKER = '@media (prefers-color-scheme: dark)';

// Ratios per pair [text token, background token, minimum]. Body text must
// reach AAA (7); the red is used for small text on cards only, so AA (4.5).
// --on-mark is the fixed dark ink printed on the highlighter in both themes.
const PAIRS = [
  ['ink', 'desk', 7],
  ['ink', 'card', 7],
  ['red', 'card', 4.5],
  ['on-mark', 'mark', 7],
];

const css = readFileSync(TOKENS_FILE, 'utf8');
const cut = css.indexOf(DARK_MARKER);
if (cut === -1) {
  console.error(`tokens.css has no "${DARK_MARKER}" block`);
  process.exit(1);
}
const palettes = { light: css.slice(0, cut), dark: css.slice(cut) };

function channel(value) {
  const c = value / 255;
  return c <= 0.03928 ? c / 12.92 : ((c + 0.055) / 1.055) ** 2.4;
}

function luminance(hex) {
  const [r, g, b] = [1, 3, 5].map((i) => channel(parseInt(hex.slice(i, i + 2), 16)));
  return 0.2126 * r + 0.7152 * g + 0.0722 * b;
}

function ratio(a, b) {
  const [hi, lo] = [luminance(a), luminance(b)].sort((x, y) => y - x);
  return (hi + 0.05) / (lo + 0.05);
}

const failures = [];
for (const [theme, block] of Object.entries(palettes)) {
  const token = (name) => block.match(new RegExp(`--${name}:\\s*(#[0-9A-Fa-f]{6})`))?.[1];
  for (const [text, background, minimum] of PAIRS) {
    const fg = token(text);
    const bg = token(background);
    if (!fg || !bg) {
      failures.push(`${theme}: --${text} or --${background} is not defined`);
      continue;
    }
    const got = ratio(fg, bg);
    if (got < minimum) {
      failures.push(`${theme}: --${text} ${fg} on --${background} ${bg} is ${got.toFixed(2)}:1, needs ${minimum}:1`);
    }
  }
}

if (failures.length > 0) {
  // bearer:disable javascript_lang_logger_leak
  console.error(`${failures.length} contrast check(s) failed:`);
  // bearer:disable javascript_lang_logger_leak
  for (const message of failures) console.error(`  - ${message}`);
  process.exit(1);
}
console.log('contrast checks passed');
