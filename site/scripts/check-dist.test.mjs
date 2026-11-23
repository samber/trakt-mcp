// Proves the dist gate can fail. Each test copies the built dist/ to a temporary
// directory, breaks it one way, runs scripts/check-dist.mjs on the copy, and
// expects exit code 1. Run `npm run build` first: the tests need dist/.
import assert from 'node:assert/strict';
import { spawnSync } from 'node:child_process';
import { cpSync, existsSync, mkdtempSync, readFileSync, rmSync, writeFileSync } from 'node:fs';
import { tmpdir } from 'node:os';
import { join } from 'node:path';
import { test } from 'node:test';
import { fileURLToPath } from 'node:url';

const SCRIPT = fileURLToPath(new URL('./check-dist.mjs', import.meta.url));
const DIST = fileURLToPath(new URL('../dist/', import.meta.url));
const GO_TOOLS_DIR = fileURLToPath(new URL('../../internal/tools/', import.meta.url));

assert.ok(existsSync(DIST), 'dist/ is missing: run `npm run build` before these tests');

/** Runs the gate on a copy of dist/ whose index.html is rewritten by `mutate`. */
function runGate({ mutate, goToolsDir = GO_TOOLS_DIR } = {}) {
  const copy = mkdtempSync(join(tmpdir(), 'dist-gate-'));
  try {
    cpSync(DIST, copy, { recursive: true });
    if (mutate) {
      const file = join(copy, 'index.html');
      writeFileSync(file, mutate(readFileSync(file, 'utf8')));
    }
    return spawnSync(process.execPath, [SCRIPT], {
      env: { ...process.env, DIST_DIR: copy, GO_TOOLS_DIR: goToolsDir },
      encoding: 'utf8',
    });
  } finally {
    rmSync(copy, { recursive: true, force: true });
  }
}

const inject = (markup) => (html) => html.replace('</head>', `${markup}</head>`);

test('the gate passes on an untouched build (positive control)', () => {
  const result = runGate();
  assert.equal(result.status, 0, result.stderr);
});

test('the gate rejects a protocol-relative stylesheet from another host', () => {
  const result = runGate({ mutate: inject('<link rel="stylesheet" href="//fonts.googleapis.com/css2?family=X">') });
  assert.equal(result.status, 1);
  assert.match(result.stderr, /disallowed host/);
});

test('the gate rejects an image whose srcset points to another host', () => {
  const result = runGate({ mutate: inject('<img srcset="https://tracker.example/a.png 1x, /b.png 2x" alt="">') });
  assert.equal(result.status, 1);
  assert.match(result.stderr, /disallowed host/);
});

test('the gate rejects a CSS @import from another host', () => {
  const result = runGate({ mutate: inject('<style>@import url(//cdn.example/x.css);</style>') });
  assert.equal(result.status, 1);
  assert.match(result.stderr, /disallowed host/);
});

test('the gate fails when the Go tools directory is missing instead of skipping the tool check', () => {
  const result = runGate({ goToolsDir: join(tmpdir(), 'no-such-dir-for-gate-test') });
  assert.equal(result.status, 1);
  assert.match(result.stderr, /internal\/tools/);
});
