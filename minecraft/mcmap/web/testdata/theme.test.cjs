'use strict';

// The theme switch, run and not read. Run by `go test ./web/` where there is
// a node, or by hand: `node web/testdata/theme.test.cjs`.
const assert = require('node:assert/strict');
const vm = require('node:vm');
const fs = require('node:fs');
const path = require('node:path');

const source = fs.readFileSync(path.join(__dirname, '..', 'theme.js'), 'utf8');

function element() {
  const attributes = {};
  const listeners = {};
  return {
    attributes, listeners, dataset: {}, children: [],
    setAttribute: (name, value) => { attributes[name] = value; },
    addEventListener: (type, fn) => { listeners[type] = fn; },
    append(child) { this.children.push(child); },
  };
}

// A page with the given holders for the switch and a record that keeps one
// look; the returned page tells of a change the way settings.js does.
function env({ theme = 'dark', holders = 1, withSettings = true } = {}) {
  const switches = Array.from({ length: holders }, element);
  const heard = [];
  const look = { theme, size: 'normal' };
  const document = {
    querySelectorAll: (selector) => (selector === '[data-theme-switch]' ? switches : []),
    createElement: () => element(),
    addEventListener: (type, fn) => heard.push([type, fn]),
  };
  const set = [];
  const mcmapSettings = withSettings ? {
    get: () => ({ ...look }),
    set: (section, value) => { set.push([section, value]); Object.assign(look, value); },
  } : undefined;
  const win = { document, mcmapSettings };
  vm.runInContext(source, vm.createContext({ ...win, window: win }), { filename: 'theme.js' });
  return { switches, set, look, tell: () => heard.filter(([type]) => type === 'mcmap:settings').forEach(([, fn]) => fn()) };
}

const pressed = (holder) => Object.fromEntries(holder.children.map((b) => [b.dataset.theme, b.attributes['aria-pressed']]));
const tests = [];
const test = (name, fn) => tests.push([name, fn]);

test('three buttons in a labelled group, the current one pressed', () => {
  const e = env({ theme: 'light' });
  assert.equal(e.switches[0].attributes.role, 'group');
  assert.equal(e.switches[0].attributes['aria-label'], 'Theme');
  assert.deepEqual(e.switches[0].children.map((b) => b.attributes['aria-label']), ['System', 'Light', 'Dark']);
  assert.deepEqual(pressed(e.switches[0]), { system: 'false', light: 'true', dark: 'false' });
});

test('a press keeps that theme and changes nothing else of the look', () => {
  const e = env({ theme: 'dark' });
  e.switches[0].children[1].listeners.click();
  assert.equal(JSON.stringify(e.set), JSON.stringify([['look', { theme: 'light', size: 'normal' }]]));
});

test('high contrast leaves every button unpressed', () => {
  const e = env({ theme: 'contrast' });
  assert.deepEqual(Object.values(pressed(e.switches[0])), ['false', 'false', 'false']);
});

test('a change from elsewhere moves every switch on the page', () => {
  const e = env({ theme: 'dark', holders: 2 });
  e.look.theme = 'system';
  e.tell();
  for (const holder of e.switches) assert.deepEqual(pressed(holder), { system: 'true', light: 'false', dark: 'false' });
});

test('a page with no record or no switch is left alone', () => {
  assert.equal(env({ withSettings: false }).switches[0].children.length, 0);
  assert.doesNotThrow(() => env({ holders: 0 }));
});

(async () => {
  let failed = 0;
  for (const [name, fn] of tests) {
    try { await fn(); console.log(`ok - ${name}`); } catch (err) { failed++; console.log(`not ok - ${name}\n${err.stack}`); }
  }
  if (failed) process.exit(1);
})();
