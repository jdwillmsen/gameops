import test from 'node:test';
import assert from 'node:assert/strict';
import { FALLBACK, SENTENCES, resultIn, sentenceFor, xboxOffered } from './signin-xbox.js';
import { readFileSync } from 'node:fs';

test('every word the server writes has a sentence of its own', () => {
  const src = readFileSync(new URL('../../auth/xbox.go', import.meta.url), 'utf8') +
    readFileSync(new URL('../../../../pkg/xbox/xbox.go', import.meta.url), 'utf8');
  const words = [...src.matchAll(/^\s*(?:xbox|Result)\w+\s*=\s*"([a-z_]+)"/gm)].map(m => m[1]);
  assert.ok(words.length >= 14, `found ${words.length} words`);
  for (const w of words) assert.ok(Object.hasOwn(SENTENCES, w), `no sentence for ${w}`);
});

test('an unknown or inherited word falls back', () => {
  for (const w of ['constructor', '__proto__', 'toString', '', null, undefined, 'x<script>']) assert.equal(sentenceFor(w), FALLBACK);
  assert.equal(sentenceFor('cancelled'), SENTENCES.cancelled);
});

test('the result is read only when there is exactly one', () => {
  assert.equal(resultIn('?xbox=cancelled'), 'cancelled');
  assert.equal(resultIn('?a=1&xbox=busy'), 'busy');
  assert.equal(resultIn('?xbox=a&xbox=b'), null);
  assert.equal(resultIn(''), null);
  assert.equal(resultIn(undefined), null);
});

test('the button is offered only when the server says so', async () => {
  assert.equal(await xboxOffered(async () => ({ passkey: true, xbox: true })), true);
  assert.equal(await xboxOffered(async () => ({ passkey: true, xbox: false })), false);
  assert.equal(await xboxOffered(async () => ({ passkey: true })), false);
  assert.equal(await xboxOffered(async () => null), false);
  assert.equal(await xboxOffered(async () => { throw new Error('offline'); }), false);
});
