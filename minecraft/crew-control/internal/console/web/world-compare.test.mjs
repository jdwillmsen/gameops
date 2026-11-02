import test from 'node:test';
import assert from 'node:assert/strict';
import { worldOf, templateOf, unitHours, delta, median, compareWorlds, METRICS, TEMPLATES, DEFAULT_WORLD, MIN_UNIT_HOURS } from './world-compare.js';
import { inFilter, taskDurations, createDashData } from './dash-data.js';

const T0 = Date.parse('2026-11-02T12:00:00Z');
const HOUR = 3_600_000;
const unit = (id, world, o = {}) => ({ id, name: id, world, online: true, dead: false, health: 20, hunger: 20, task: null, ...o });
const ev = (seq, ms, u, kind, extra = {}) => ({ seq, at: new Date(T0 + ms).toISOString(), unit: u, kind, level: 'info', text: kind, ...extra });
const F = (o = {}) => ({ unit: '', from: T0, to: T0 + HOUR, ...o });
// An hour of 5 s samples with these units online per world.
const hourOf = online => Array.from({ length: 720 }, (_, i) => ({ t: T0 + i * 5000, online }));

const cell = (w, key) => w.cells.find(c => c.key === key);

test('worlds and templates come only from the closed vocabularies', () => {
  assert.equal(worldOf({}), DEFAULT_WORLD);
  assert.equal(worldOf({ world: '' }), DEFAULT_WORLD);
  assert.equal(worldOf({ world: 'fwb' }), 'fwb');
  for (const bad of ['Fwb', '<b>x</b>', 'a'.repeat(17), 7, 'a b']) assert.equal(worldOf({ world: bad }), 'other', String(bad));
  assert.equal(templateOf('haul'), 'haul');
  assert.equal(templateOf('Steve at 1 2 3'), 'other');
  assert.equal(templateOf(undefined), 'other');
  assert.ok(TEMPLATES.every(t => templateOf(t) === t));
});

test('unitHours credits each sample to the time until the next, and never across a gap', () => {
  const s = [{ t: 0, online: { lab: 2 } }, { t: 10_000, online: { lab: 2, fwb: 1 } }, { t: 20_000, online: { lab: 2, fwb: 1 } }];
  const h = unitHours(s, 0, 30_000);
  assert.ok(Math.abs(h.get('lab') - (2 * 10_000 + 2 * 10_000 + 2 * 10_000) / HOUR) < 1e-9);
  assert.ok(Math.abs(h.get('fwb') - (1 * 10_000 + 1 * 10_000) / HOUR) < 1e-9);
  // A stream that dropped for ten minutes was not watched for ten minutes.
  const gap = unitHours([{ t: 0, online: { lab: 1 } }, { t: 600_000, online: { lab: 1 } }], 0, 700_000);
  assert.ok(gap.get('lab') < 60_000 / HOUR + 1e-9);
  assert.equal(unitHours([{ t: 0 }], 0, 10).size, 0, 'a sample from before worlds existed adds nothing');
  assert.equal(unitHours(s, 100_000, 200_000).size, 0, 'samples outside the window do not count');
});

test('delta says above, near or below with a signed number', () => {
  assert.deepEqual(delta(12, 10), { value: 2, word: 'above' });
  assert.deepEqual(delta(8, 10), { value: -2, word: 'below' });
  assert.equal(delta(10.5, 10).word, 'near');
  assert.equal(delta(0, 0).word, 'near');
  assert.equal(delta(0.5, 0).word, 'above');
  assert.equal(delta(null, 1), null);
  assert.equal(delta(1, null), null);
  assert.equal(median([3, 1, null, 2]), 2);
  assert.equal(median([]), null);
});

function twoWorlds() {
  const units = [unit('a', 'lab'), unit('b', 'lab'), unit('c', 'fwb', { health: 10 }), unit('d', 'fwb', { online: false })];
  const feed = [];
  let seq = 0;
  for (let i = 0; i < 40; i++) feed.push(ev(++seq, i * 60_000, 'a', 'task_done', { world: 'lab', template: 'gather_and_deposit' }));
  for (let i = 0; i < 10; i++) feed.push(ev(++seq, i * 60_000, 'c', 'task_done', { world: 'fwb', template: 'gather_and_deposit' }));
  feed.push(ev(++seq, 5000, 'c', 'task_failed', { world: 'fwb' }), ev(++seq, 6000, 'c', 'task_failed', { world: 'fwb' }));
  feed.push(ev(++seq, 7000, 'c', 'died', { world: 'fwb' }), ev(++seq, 8000, 'a', 'stuck', { world: 'lab' }));
  const samples = hourOf({ lab: 2, fwb: 1 });
  return { units, feed, samples, durations: [{ unit: 'a', secs: 20, template: 'gather_and_deposit' }, { unit: 'c', secs: 100, template: 'gather_and_deposit' }], filter: F(), inFilter };
}

test('worlds are compared per unit-hour with a delta against the median of the worlds', () => {
  const m = compareWorlds(twoWorlds());
  assert.equal(m.state, 'ready');
  assert.deepEqual(m.worlds.map(w => w.world), ['fwb', 'lab']);
  const [fwb, lab] = m.worlds;
  assert.deepEqual([lab.online, lab.total, fwb.online, fwb.total], [2, 2, 1, 2]);
  // lab: 40 done over 2 unit-hours = 20/h; fwb: 10 over 1 unit-hour = 10/h.
  assert.equal(cell(lab, 'doneRate').text, '20.0');
  assert.equal(cell(fwb, 'doneRate').text, '10.0');
  assert.deepEqual(m.medianRow.find(c => c.key === 'doneRate'), { key: 'doneRate', text: '15.0' });
  assert.deepEqual(cell(lab, 'doneRate').delta, { text: '+5.0', word: 'above' });
  assert.deepEqual(cell(fwb, 'doneRate').delta, { text: '-5.0', word: 'below' });
  assert.equal(cell(fwb, 'failRate').text, '17%');
  assert.equal(cell(lab, 'failRate').text, '0%');
  assert.equal(cell(fwb, 'deathRate').text, '1.00');
  assert.equal(cell(lab, 'stallRate').text, '0.50');
  assert.equal(cell(fwb, 'medianSecs').text, '2 min');
  assert.equal(cell(lab, 'medianSecs').text, '20 s');
  assert.equal(cell(fwb, 'meanHealth').text, '10.0', 'an offline unit has no health to average');
  assert.equal(cell(lab, 'meanHealth').text, '20.0');
  // Every delta is a number and a word, never a colour.
  for (const w of m.worlds) for (const c of w.cells) if (c.delta) assert.match(c.delta.word, /^(above|near|below)$/);
});

test('the chart is done per unit-hour by template, one series per world', () => {
  const m = compareWorlds(twoWorlds());
  assert.deepEqual(m.chart.categories, ['gather_and_deposit']);
  assert.deepEqual(m.chart.series.map(s => [s.key, s.values[0]]), [['fwb', 10], ['lab', 20]]);
  const noEvents = compareWorlds({ ...twoWorlds(), feed: [] });
  assert.deepEqual(noEvents.chart.categories, []);
  assert.equal(noEvents.state, 'ready', 'units alone are enough to show the table');
});

test('a world with too little watched time shows no rate rather than a noisy one', () => {
  const t = twoWorlds();
  const m = compareWorlds({ ...t, samples: [{ t: T0, online: { lab: 2, fwb: 1 } }] });
  for (const w of m.worlds) assert.equal(cell(w, 'doneRate').text, '-');
  assert.ok(MIN_UNIT_HOURS > 20_000 / HOUR, 'a single sample is below the bar');
  assert.equal(cell(m.worlds[0], 'failRate').text, '17%', 'a share needs no exposure time');
});

test('a lone world is shown without deltas, and the unit filter and an unknown world are respected', () => {
  const t = twoWorlds();
  const lone = compareWorlds({ ...t, units: t.units.filter(u => u.world === 'lab'), feed: t.feed.filter(e => e.world === 'lab') });
  assert.equal(lone.worlds.length, 1);
  assert.ok(lone.worlds[0].cells.every(c => c.delta === null));
  assert.match(lone.announce, /^1 world\./);

  // An event with no world is placed by its unit; one from an unknown unit is skipped.
  const bare = compareWorlds({ ...t, feed: [ev(1, 0, 'a', 'task_done', { template: 'haul' }), ev(2, 0, 'ghost', 'task_done', { template: 'haul' })] });
  assert.equal(bare.chart.series.find(s => s.key === 'lab').values[0] > 0, true);
  assert.equal(bare.chart.series.find(s => s.key === 'fwb').values[0], 0);

  // A hostile world slug is shown as "other", not as itself.
  const evil = compareWorlds({ ...t, units: [unit('a', '<img src=x>')], feed: [] });
  assert.deepEqual(evil.worlds.map(w => w.world), ['other']);
});

test('no snapshot yet is loading, and nothing at all to compare is empty', () => {
  const none = { units: [], feed: [], samples: [], durations: [], filter: F(), inFilter };
  assert.equal(compareWorlds(none).state, 'loading');
  const samplesOnly = compareWorlds({ ...none, samples: hourOf({}) });
  assert.equal(samplesOnly.state, 'empty');
});

test('the announcement names each world, its online count, its rate and where it sits', () => {
  const m = compareWorlds(twoWorlds());
  assert.equal(m.announce, '2 worlds compared. fwb: 1 of 2 units online, done rate below the median of the worlds. lab: 2 of 2 units online, done rate above the median of the worlds.');
  assert.doesNotMatch(m.announce, /\d+\.\d/, 'a moving rate must not be in the live region');
  assert.doesNotThrow(() => JSON.stringify(m));
});

test('it composes with the dashboard data: samples carry the connected units per world', () => {
  const data = createDashData();
  data.onUnits([unit('a', 'lab'), unit('b', 'fwb'), unit('c', 'fwb', { online: false })], T0);
  assert.deepEqual(data.samples()[0].online, { lab: 1, fwb: 1 });
  data.onEvents([ev(1, 0, 'a', 'task_started', { task: 't', template: 'haul' }), ev(2, 3000, 'a', 'task_done', { task: 't', template: 'haul' })]);
  assert.deepEqual(taskDurations(data.feed(), F()).map(d => [d.unit, d.template]), [['a', 'haul']]);
  const m = compareWorlds({ units: data.units(), feed: data.feed(), samples: data.samples(), durations: taskDurations(data.feed(), F()), filter: F(), inFilter });
  assert.equal(m.worlds.length, 2);
  assert.equal(METRICS.length, m.worlds[0].cells.length);
});

test('events from before the first sample are not divided by the sampled hours', () => {
  // The feed reaches back 50 minutes; this page has only watched the last
  // three, so only those three minutes' events can be divided by their hours.
  const start = 50 * 60_000;
  const feed = [];
  for (let i = 0; i < 50; i++) feed.push(ev(i + 1, i * 60_000, 'a', 'task_done', { world: 'lab', template: 'gather_and_deposit' }));
  for (let i = 0; i < 3; i++) feed.push(ev(100 + i, start + i * 60_000, 'a', 'task_done', { world: 'lab', template: 'gather_and_deposit' }));
  const samples = Array.from({ length: 9 }, (_, i) => ({ t: T0 + start + i * 20_000, online: { lab: 1 } }));
  const m = compareWorlds({ units: [unit('a', 'lab')], feed, samples, durations: [], filter: F(), inFilter });
  const hours = unitHours(samples, T0 + start, T0 + HOUR);
  const watched = hours.get('lab');
  assert.ok(watched >= MIN_UNIT_HOURS, 'enough watched time to show a rate');
  // 3 events in the watched span, not 53: the old arithmetic reported ~16x more.
  assert.equal(cell(m.worlds[0], 'doneRate').text, METRICS.find(x => x.key === 'doneRate').fmt(3 / watched));
});
