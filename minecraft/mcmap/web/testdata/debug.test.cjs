'use strict';

// The troubleshooting tools, run and not read: the debug panel costs
// nothing while it is off and is torn down when it is turned off again, a
// report holds no name and no place unless asked, scripts of two releases
// are told apart, and the fingerprint is the server's. Run by `go test
// ./web/` where there is a node, or by hand: `node web/testdata/debug.test.cjs`.
const assert = require('node:assert/strict');
const vm = require('node:vm');
const fs = require('node:fs');
const path = require('node:path');
const { page } = require('./dom.cjs');

const tests = [];
const test = (name, fn) => tests.push([name, fn]);
const source = (file) => fs.readFileSync(path.join(__dirname, '..', file), 'utf8');

const SECRET = { gamertag: 'SecretGamer42', view: 'My Secret Base', waypoint: 'Hidden Waypoint', x: 123456, z: -98765 };

function env({ search = '', href = `http://map.example/#overworld/${SECRET.x}/${SECRET.z}/3`, storage = new Map() } = {}) {
  const { win, doc, settings } = page(storage, { files: ['settings.js'] });
  const made = new Map();
  const plain = doc.getElementById;
  const kind = (id) => (/-(fold|close|copy|download|record|help-copy|help-download)$/.test(id) ? 'button' : /(position|-on|report-on)$/.test(id) ? 'input' : 'div');
  doc.getElementById = (id) => plain(id) || (made.has(id) ? made.get(id) : (made.set(id, Object.assign(doc.createElement(kind(id)), { id })), made.get(id)));
  const panel = doc.getElementById('debug');
  panel.hidden = true;
  panel.querySelectorAll = () => [];
  doc.scripts = [];

  const counts = { timers: new Set(), frames: new Set(), mapOn: 0, mapOff: 0, docOn: [] };
  let next = 1;
  win.setInterval = () => { const id = next++; counts.timers.add(id); return id; };
  win.clearInterval = (id) => counts.timers.delete(id);
  win.requestAnimationFrame = () => { const id = next++; counts.frames.add(id); return id; };
  win.cancelAnimationFrame = (id) => counts.frames.delete(id);
  const addDoc = doc.addEventListener;
  doc.addEventListener = (type, fn) => { counts.docOn.push(type); addDoc(type, fn); };

  const centre = { lat: SECRET.z, lng: SECRET.x };
  const map = {
    _loaded: true,
    on() { counts.mapOn += 1; return this; },
    off() { counts.mapOff += 1; return this; },
    eachLayer() {},
    getZoom: () => 3,
    getMinZoom: () => -4,
    getMaxZoom: () => 7,
    getSize: () => ({ x: 800, y: 600 }),
    getCenter: () => centre,
    getBounds: () => ({ getWest: () => SECRET.x - 50, getEast: () => SECRET.x + 50, getNorth: () => SECRET.z - 50, getSouth: () => SECRET.z + 50 }),
    getContainer: () => doc.getElementById('map'),
  };
  Object.assign(win, {
    location: { search, href, pathname: '/' },
    URLSearchParams, URL, TextEncoder, Uint8Array,
    performance: { now: () => 1 },
    navigator: { userAgent: 'TestAgent/1.0', platform: 'Test', language: 'en', maxTouchPoints: 0, onLine: true },
    screen: { width: 1920, height: 1080 },
    outerWidth: 1440, outerHeight: 900,
    L: { GridLayer: class {} },
  });
  win.mcmap = { map, dimension: () => 'overworld', zoom: { running: () => null }, icons: { density: () => 1 } };
  vm.runInContext(source('diag.js'), win, { filename: 'diag.js' });
  const before = counts.docOn.length;
  return {
    win, doc, settings, counts, map,
    load: () => {
      vm.runInContext(source('debug.js'), win, { filename: 'debug.js' });
      return counts.docOn.slice(before);
    },
  };
}

test('with the panel off it holds one listener and no timer, frame loop or map listener', () => {
  const e = env();
  const listeners = e.load();
  assert.deepEqual(listeners, ['mcmap:settings']);
  assert.equal(e.counts.timers.size, 0);
  assert.equal(e.counts.frames.size, 0);
  assert.equal(e.counts.mapOn, 0);
  assert.equal(e.win.mcmap.debug.on(), false);
});

test('turned on it runs, and turned off it leaves nothing behind', () => {
  const e = env();
  e.load();
  e.settings.set('debug', { on: true });
  assert.equal(e.win.mcmap.debug.on(), true);
  assert.equal(e.counts.timers.size, 1);
  assert.equal(e.counts.frames.size, 1);
  assert.ok(e.counts.mapOn > 0);
  e.settings.set('debug', { on: false });
  assert.equal(e.counts.timers.size, 0);
  assert.equal(e.counts.frames.size, 0);
  assert.equal(e.counts.mapOn, e.counts.mapOff, 'every map listener taken off');
  e.win.mcmap.debug.toggle();
  assert.equal(e.win.mcmap.debug.on(), true, 'the shortcut turns it on');
});

test('a link turns the panel and the report tools on or off, and the choice is kept', () => {
  const storage = new Map();
  const on = env({ search: '?debug=1&diagnostics=true', storage });
  on.load();
  assert.equal(on.win.mcmap.debug.on(), true);
  assert.deepEqual([on.settings.get('debug').on, on.settings.get('debug').report], [true, true]);
  const again = env({ storage });
  again.load();
  assert.equal(again.win.mcmap.debug.on(), true, 'remembered with no parameter');
  const off = env({ search: '?debug=0', storage });
  off.load();
  assert.equal(off.win.mcmap.debug.on(), false);
  assert.equal(off.settings.get('debug').report, true, 'what the link does not mention is left');
});

test('a report holds no name and no place unless the position is asked for', async () => {
  const e = env();
  e.load();
  e.settings.set('views', { list: [{ id: 'abc', name: SECRET.view, layers: { 'live/players': true }, players: { only: null, hidden: [SECRET.gamertag] }, place: { d: 'overworld', x: SECRET.x, z: SECRET.z, zoom: 4.5 } }] });
  e.settings.set('layers', { 'live#players': { only: SECRET.gamertag, hidden: [SECRET.gamertag] }, 'markers#waypoints': { only: null, hidden: [SECRET.waypoint] }, 'live/players': true });
  const bare = await e.win.mcmap.debug.report(false);
  for (const secret of Object.values(SECRET)) assert.ok(!bare.includes(String(secret)), `the report names ${secret}`);
  const report = JSON.parse(bare);
  assert.equal(report.includesPosition, false);
  assert.ok(!('Place' in report.panel), 'no place group');
  assert.equal(report.settings.values.views.count, 1);
  assert.equal(report.settings.values.views.saved[0].place.position, 'omitted');
  assert.equal(report.settings.values.views.saved[0].place.zoom, 4.5);
  assert.ok(report.link.endsWith('#overworld/…/…/3'), report.link);
  assert.equal(report.browser.userAgent, 'TestAgent/1.0');
  assert.equal(report.layers.switches.on, 1);

  const full = await e.win.mcmap.debug.report(true);
  assert.ok(full.includes(String(SECRET.x)) && full.includes(String(SECRET.z)), 'the place is there when asked for');
  for (const name of [SECRET.gamertag, SECRET.view, SECRET.waypoint]) assert.ok(!full.includes(name), `even then it names ${name}`);
  assert.equal(JSON.parse(full).includesPosition, true);
});

test('scripts of two releases are told apart', () => {
  const { mixed } = env().win.mcmapDiag;
  const served = { 'app.js': 'aaaaaaaa', 'live.js': 'bbbbbbbb', 'index.html': 'cccccccc' };
  assert.equal(mixed({ 'app.js': 'aaaaaaaa', 'live.js': 'bbbbbbbb', 'index.html': 'cccccccc' }, served).verdict, 'current');
  const some = mixed({ 'app.js': 'aaaaaaaa', 'live.js': '11111111', 'index.html': 'cccccccc' }, served);
  assert.match(some.verdict, /^mixed/);
  assert.deepEqual([...some.stale], ['live.js']);
  assert.match(mixed({ 'app.js': '1', 'live.js': '2' }, served).verdict, /^old/);
  assert.deepEqual([...mixed({ 'extra.js': 'x' }, served).unknown], ['extra.js']);
});

test('the fingerprint is the one the server makes', () => {
  const { fingerprint } = env().win.mcmapDiag;
  const bytes = (s) => new Uint8Array(Buffer.from(s));
  assert.equal(fingerprint(bytes('')), '811c9dc5');
  assert.equal(fingerprint(bytes('a')), 'e40c292c');
  assert.equal(fingerprint(bytes('foobar')), 'bf9cf968');
});

test('the log is capped, and an event holds only what it was told', () => {
  const { keep } = env().win.mcmapDiag;
  for (let i = 0; i < 500; i++) keep.log('zoom', `to ${i}`);
  const events = keep.events();
  assert.equal(events.length, 200);
  assert.equal(events.at(-1).text, 'to 499');
  keep.log('x', 'y'.repeat(1000));
  assert.ok(keep.events().at(-1).text.length <= 160);
});

async function main() {
  let failed = 0;
  for (const [name, fn] of tests) {
    try {
      await fn();
      console.log(`ok   ${name}`);
    } catch (err) {
      failed += 1;
      console.log(`FAIL ${name}\n     ${String(err && err.stack ? err.stack : err).split('\n').slice(0, 6).join('\n     ')}`);
    }
  }
  console.log(`${tests.length - failed} of ${tests.length} passed`);
  process.exitCode = failed === 0 ? 0 : 1;
}

main();
