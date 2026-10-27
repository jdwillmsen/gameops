import test from 'node:test';
import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import { REGISTRY, UNREGISTERED } from './views.js';
import { VIEWS } from './state.js';

const html = readFileSync(new URL('./index.html', import.meta.url), 'utf8');
const consoleJs = readFileSync(new URL('./console.js', import.meta.url), 'utf8');

test('every registered view has a title and a single-character toggle key', () => {
  for (const r of REGISTRY) {
    assert.ok(r.title && r.title.trim(), `${r.view} has no title`);
    assert.match(r.key || '', /^[a-z]$/, `${r.view} has no toggle key`);
  }
});

test('keys, views and toggle ids are unique', () => {
  for (const f of ['key', 'view', 'toggle']) {
    const vals = REGISTRY.map(r => r[f]);
    assert.equal(new Set(vals).size, vals.length, `duplicate ${f}`);
  }
});

test('registry covers every state view', () => {
  const known = [...REGISTRY.map(r => r.view), ...UNREGISTERED].sort();
  assert.deepEqual(known, [...VIEWS].sort());
});

test('index.html toggles carry the registered key in their tooltip', () => {
  for (const r of REGISTRY) {
    const m = html.match(new RegExp(`<button id="${r.toggle}"[^>]*title="([^"]*)"`));
    assert.ok(m, `${r.toggle} missing from index.html`);
    assert.ok(m[1].includes(`(${r.key})`), `${r.toggle} title "${m[1]}" lacks (${r.key})`);
    assert.ok(m[1].includes(r.title), `${r.toggle} title "${m[1]}" lacks "${r.title}"`);
  }
});

test('console.js handles every registered key', () => {
  for (const r of REGISTRY) {
    assert.ok(consoleJs.includes(`ev.key === '${r.key}') ${r.handler}()`), `key ${r.key} does not call ${r.handler} (${r.view})`);
  }
});
