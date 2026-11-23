import assert from 'node:assert/strict';
import { test } from 'node:test';
import { copyText } from './copy.ts';

const URL_TO_COPY = 'https://mcp.example.test/mcp';

test('copyText reports "copied" and writes the exact text when the clipboard accepts it', async () => {
  const written: string[] = [];
  const clipboard = {
    writeText: async (text: string) => {
      written.push(text);
    },
  };

  assert.equal(await copyText(URL_TO_COPY, clipboard), 'copied');
  assert.deepEqual(written, [URL_TO_COPY]);
});

test('copyText reports "unavailable" when the clipboard rejects the write', async () => {
  const clipboard = { writeText: () => Promise.reject(new DOMException('denied', 'NotAllowedError')) };

  assert.equal(await copyText(URL_TO_COPY, clipboard), 'unavailable');
});

test('copyText reports "unavailable" when writeText throws synchronously', async () => {
  const clipboard = {
    writeText: (): Promise<void> => {
      throw new TypeError('insecure context');
    },
  };

  assert.equal(await copyText(URL_TO_COPY, clipboard), 'unavailable');
});

test('copyText calls onCopied once after a successful write', async () => {
  let calls = 0;
  const clipboard = { writeText: async () => {} };

  assert.equal(await copyText(URL_TO_COPY, clipboard, () => calls++), 'copied');
  assert.equal(calls, 1);
});

test('copyText does not call onCopied when the write is refused or the API is missing', async () => {
  let calls = 0;
  const refusing = { writeText: () => Promise.reject(new DOMException('denied', 'NotAllowedError')) };

  await copyText(URL_TO_COPY, refusing, () => calls++);
  await copyText(URL_TO_COPY, undefined, () => calls++);
  assert.equal(calls, 0);
});

test('copyText still reports "copied" when onCopied throws', async () => {
  const clipboard = { writeText: async () => {} };
  const onCopied = () => {
    throw new Error('hook failed');
  };

  assert.equal(await copyText(URL_TO_COPY, clipboard, onCopied), 'copied');
});

test('copyText reports "unavailable" when there is no clipboard API', async () => {
  assert.equal(await copyText(URL_TO_COPY, undefined), 'unavailable');
});
