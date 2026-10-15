'use strict';

// The Xbox sign-in's page script, run and not read. Run by `go test ./web/`
// where there is a node, or by hand: `node web/testdata/xbox.test.cjs`.
const assert = require('node:assert/strict');
const vm = require('node:vm');
const fs = require('node:fs');
const path = require('node:path');
const { page } = require('./dom.cjs');

const tests = [];
const test = (name, fn) => tests.push([name, fn]);
const source = fs.readFileSync(path.join(__dirname, '..', 'xbox.js'), 'utf8');

// A page with xbox.js in it, at an address, with a server that answers
// api/config as given (or not at all).
function env({ search = '', config = { login: true, xbox: true }, down = false } = {}) {
  const { win, doc } = page(new Map(), { files: [] });
  const made = new Map();
  const plain = doc.getElementById;
  doc.getElementById = (id) => plain(id) || (made.has(id) ? made.get(id) : (made.set(id, Object.assign(doc.createElement(id === 'login-xbox' ? 'form' : 'p'), { id })), made.get(id)));
  doc.getElementById('login-xbox').hidden = true;
  const replaced = [];
  win.location = { search, pathname: '/', hash: '' };
  win.history = { replaceState: (...args) => replaced.push(args) };
  win.fetch = async (url) => {
    if (down || url !== 'api/config') throw new Error('unreachable');
    return { ok: true, json: async () => config };
  };
  win.URLSearchParams = URLSearchParams; // Node's; a fresh context has none
  vm.runInContext(source, win, { filename: 'xbox.js' });
  return { win, el: (id) => doc.getElementById(id), replaced };
}

test('the button is offered only when the server says it is on', async () => {
  const on = env();
  await on.win.mcmapXbox.ready;
  assert.equal(on.el('login-xbox').hidden, false);
  for (const config of [{ login: true }, { login: true, xbox: false }, { login: true, xbox: 'yes' }]) {
    const off = env({ config });
    await off.win.mcmapXbox.ready;
    assert.equal(off.el('login-xbox').hidden, true, JSON.stringify(config));
  }
  const gone = env({ down: true });
  await gone.win.mcmapXbox.ready;
  assert.equal(gone.el('login-xbox').hidden, true, 'no answer, no button');
});

test('a word from the server becomes its sentence and leaves the address', async () => {
  const e = env({ search: '?xbox=stranger' });
  await e.win.mcmapXbox.ready;
  assert.match(e.el('login-xbox-note').textContent, /not one this server knows/);
  assert.equal(e.replaced.length, 1);
  assert.equal(e.replaced[0][2], '/');
});

test('every word the server writes has a sentence of its own', () => {
  const { sentences, sentenceFor } = env().win.mcmapXbox;
  const server = fs.readFileSync(path.join(__dirname, '..', '..', 'internal', 'server', 'xbox.go'), 'utf8') + fs.readFileSync(path.join(__dirname, '..', '..', 'internal', 'xbox', 'xbox.go'), 'utf8');
  const words = [...server.matchAll(/(?:xbox\w+|Result\w+)\s*=\s*"([a-z_]+)"/g)].map((m) => m[1]).filter((w) => w !== 'ok');
  assert.ok(words.length >= 12, `found only ${words.length} words`);
  for (const w of words) assert.ok(Object.hasOwn(sentences, w), `${w} has no sentence`);
  for (const w of Object.keys(sentences)) assert.notEqual(sentenceFor(w), sentenceFor('nonsense'), w);
});

test('nothing in the address is ever shown as it came', async () => {
  for (const search of ['?xbox=<img src=x onerror=alert(1)>', '?xbox=constructor', '?xbox=__proto__', '?xbox=toString', '?xbox=a&xbox=stranger', '?xbox=']) {
    const e = env({ search });
    await e.win.mcmapXbox.ready;
    const text = e.el('login-xbox-note').textContent;
    assert.ok(!/[<>]|onerror|function|Object/.test(text), `${search} showed ${text}`);
  }
  const e = env({ search: '?xbox=<b>hi</b>' });
  await e.win.mcmapXbox.ready;
  assert.equal(e.el('login-xbox-note').textContent, e.win.mcmapXbox.sentenceFor('x'));
});

test('no word, no sentence and the address is left alone', async () => {
  const e = env({ search: '?a=1' });
  await e.win.mcmapXbox.ready;
  assert.equal(e.el('login-xbox-note').textContent, '');
  assert.equal(e.replaced.length, 0);
});

test('other parts of the address stay when the word goes', () => {
  const { without, resultIn } = env().win.mcmapXbox;
  assert.equal(without('?xbox=stranger&x=1'), '?x=1');
  assert.equal(without('?xbox=stranger'), '');
  assert.equal(resultIn('?x=1'), null);
});

(async () => {
  let failed = 0;
  for (const [name, fn] of tests) {
    try { await fn(); console.log(`ok ${name}`); } catch (err) { failed++; console.log(`not ok ${name}\n${err.stack}`); }
  }
  if (failed) process.exit(1);
})();
