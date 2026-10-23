import test from 'node:test';
import assert from 'node:assert/strict';
import {
  stateOf, taskKind, duration, sinceLabel, sortRows, filterRows, emptyFilter, isFiltered, summarize,
  parseParams, formatParams, moveFocus, nextAttention, createBoard, trailLabels, WINDOW, SAMPLE_MS, STALE_MS, GAP_MS, STUCK_HOLD_MS,
} from './overview.js';

const u = (id, extra = {}) => ({ id, name: id, online: true, dead: false, health: 20, hunger: 20, task: null, ...extra });
const task = (extra = {}) => ({ id: 't1', template: 'gather_and_deposit', state: 'running', item: 'oak_log', qty: 8, progress: 2, ...extra });
const ev = (seq, unit, kind, extra = {}) => ({ seq, at: new Date(1_000_000 + seq * 1000).toISOString(), unit, name: unit, kind, level: 'info', text: kind, ...extra });

// A row as the board would build it, for the pure functions.
const row = (id, extra = {}) => ({ id, name: id, index: Number(id.replace(/\D/g, '')) || 1, state: 'idle', since: null, kind: 'none', taskText: 'idle', health: 20, hunger: 20, lowHealth: false, lowHunger: false, pos: null, trail: [], ...extra });

test('stateOf: offline beats dead beats a task; stuck needs a task', () => {
  assert.equal(stateOf(u('a', { online: false, dead: true, task: task() })), 'offline');
  assert.equal(stateOf(u('a', { dead: true, task: task() })), 'dead');
  assert.equal(stateOf(u('a', { task: task() })), 'working');
  assert.equal(stateOf(u('a', { task: task() }), true), 'stuck');
  assert.equal(stateOf(u('a'), true), 'idle');
});

test('taskKind shortens the gather template and treats offline as no task', () => {
  assert.equal(taskKind(u('a', { task: task() })), 'gather');
  assert.equal(taskKind(u('a', { task: task({ template: 'haul' }) })), 'haul');
  assert.equal(taskKind(u('a')), 'none');
  assert.equal(taskKind(u('a', { online: false, task: task() })), 'none');
});

test('duration and sinceLabel: a page-open floor is marked', () => {
  assert.equal(duration(-5), '0s');
  assert.equal(duration(42_000), '42s');
  assert.equal(duration(65_000), '1m 05s');
  assert.equal(duration(3_725_000), '1h 02m');
  assert.equal(duration(90_000_000), '1d 01h');
  assert.equal(sinceLabel({ at: 0, exact: true }, 10_000), '10s');
  assert.equal(sinceLabel({ at: 0, exact: false }, 10_000), '≥10s');
  assert.equal(sinceLabel(null, 10_000), '');
});

test('sortRows: unknown values go last both ways, ties keep roster order', () => {
  const rows = [row('c1', { health: 10 }), row('c2', { health: null }), row('c3', { health: 4 }), row('c4', { health: 10 })];
  assert.deepEqual(sortRows(rows, 'health').map(r => r.id), ['c3', 'c1', 'c4', 'c2']);
  assert.deepEqual(sortRows(rows, 'health', 'desc').map(r => r.id), ['c1', 'c4', 'c3', 'c2']);
  assert.deepEqual(sortRows(rows, 'unit', 'desc').map(r => r.id), ['c4', 'c3', 'c2', 'c1']);
});

test('sortRows by state puts the worst first and by since the longest wait', () => {
  const rows = [row('c1', { state: 'idle', since: { at: 500 } }), row('c2', { state: 'dead', since: { at: 900 } }), row('c3', { state: 'stuck', since: { at: 100 } }), row('c4', { state: 'working', since: null })];
  assert.deepEqual(sortRows(rows, 'state').map(r => r.id), ['c2', 'c3', 'c4', 'c1']);
  // Oldest entry time is the longest time in state.
  assert.deepEqual(sortRows(rows, 'since', 'desc').map(r => r.id), ['c3', 'c1', 'c2', 'c4']);
  assert.deepEqual(sortRows(rows, 'since', 'asc').map(r => r.id), ['c2', 'c1', 'c3', 'c4']);
});

test('sortRows by task sorts idle units after named tasks', () => {
  const rows = [row('c1'), row('c2', { kind: 'haul', taskText: 'haul' }), row('c3', { kind: 'gather', taskText: 'gather 1/8' })];
  assert.deepEqual(sortRows(rows, 'task').map(r => r.id), ['c3', 'c2', 'c1']);
});

test('filterRows: text, state, kind and level cuts combine', () => {
  const rows = [
    row('c1', { state: 'working', kind: 'gather', taskText: 'gather 3/8', health: 5 }),
    row('c2', { state: 'idle', health: 20, hunger: 4 }),
    row('c3', { state: 'stuck', kind: 'haul', taskText: 'haul', health: null }),
    row('c4', { state: 'offline', name: 'Birch' }),
  ];
  const f = patch => ({ ...emptyFilter(), ...patch });
  assert.equal(filterRows(rows, f({})).length, 4);
  assert.deepEqual(filterRows(rows, f({ q: 'GATHER' })).map(r => r.id), ['c1']);
  assert.deepEqual(filterRows(rows, f({ q: 'birch' })).map(r => r.id), ['c4']);
  assert.deepEqual(filterRows(rows, f({ q: 'stuck' })).map(r => r.id), ['c3']);
  assert.deepEqual(filterRows(rows, f({ states: ['idle', 'stuck'] })).map(r => r.id), ['c2', 'c3']);
  assert.deepEqual(filterRows(rows, f({ kind: 'haul' })).map(r => r.id), ['c3']);
  assert.deepEqual(filterRows(rows, f({ hp: 6 })).map(r => r.id), ['c1']);
  assert.deepEqual(filterRows(rows, f({ food: 6 })).map(r => r.id), ['c2']);
  assert.deepEqual(filterRows(rows, f({ states: ['working'], hp: 10 })).map(r => r.id), ['c1']);
  assert.equal(isFiltered(f({})), false);
  assert.equal(isFiltered(f({ hp: 6 })), true);
});

test('summarize counts states and the feed window', () => {
  const rows = [row('c1', { state: 'working' }), row('c2', { state: 'working' }), row('c3', { state: 'idle' }), row('c4', { state: 'stuck' }), row('c5', { state: 'dead' }), row('c6', { state: 'offline' })];
  const feed = [ev(1, 'c1', 'task_done'), ev(2, 'c1', 'task_done'), ev(3, 'c2', 'task_failed'), ev(4, 'c2', 'task_cancelled'), ev(5, 'c3', 'online')];
  const s = summarize(rows, feed);
  assert.deepEqual({ ...s, since: undefined }, { total: 6, online: 5, working: 2, idle: 1, stuck: 1, dead: 1, offline: 1, attention: 3, done: 2, failed: 1, events: 5, since: undefined });
  assert.equal(s.since, feed[0].at);
  assert.deepEqual(summarize([], []).done, 0);
});

test('params round-trip and drop anything unknown', () => {
  const p = parseParams('?view=fleet&sort=health&dir=desc&q=oak&st=stuck,dead,bogus,stuck&task=haul&hp=6&food=15');
  assert.deepEqual(p, { sort: 'health', dir: 'desc', filter: { q: 'oak', states: ['stuck', 'dead'], kind: 'haul', hp: 6, food: 15 } });
  assert.deepEqual(parseParams(`?${formatParams(p)}`), p);
  const junk = parseParams('?sort=drop&dir=up&hp=7&food=x&st=x');
  assert.deepEqual(junk, { sort: 'unit', dir: 'asc', filter: emptyFilter() });
});

test('formatParams omits defaults and keeps a non-default direction', () => {
  const base = parseParams('');
  assert.equal(formatParams(base), '');
  assert.equal(formatParams({ ...base, sort: 'since', dir: 'desc' }), 'sort=since');
  assert.equal(formatParams({ ...base, sort: 'since', dir: 'asc' }), 'sort=since&dir=asc');
  assert.equal(formatParams({ ...base, dir: 'desc' }), 'sort=unit&dir=desc');
});

test('moveFocus clamps at the ends and survives an unknown or empty list', () => {
  const ids = ['a', 'b', 'c', 'd'];
  assert.equal(moveFocus(ids, 'a', 'ArrowDown'), 'b');
  assert.equal(moveFocus(ids, 'd', 'ArrowDown'), 'd');
  assert.equal(moveFocus(ids, 'a', 'ArrowUp'), 'a');
  assert.equal(moveFocus(ids, 'c', 'k'), 'b');
  assert.equal(moveFocus(ids, 'b', 'j'), 'c');
  assert.equal(moveFocus(ids, 'a', 'PageDown', 2), 'c');
  assert.equal(moveFocus(ids, 'd', 'PageUp', 3), 'a');
  assert.equal(moveFocus(ids, 'c', 'Home'), 'a');
  assert.equal(moveFocus(ids, 'a', 'End'), 'd');
  assert.equal(moveFocus(ids, 'zzz', 'ArrowDown'), 'a');
  assert.equal(moveFocus(ids, 'b', 'x'), 'b');
  assert.equal(moveFocus([], 'a', 'ArrowDown'), '');
});

test('moveFocus from a row that is not on screen: forward keys start at the top, backward at the bottom', () => {
  const ids = ['a', 'b', 'c', 'd'];
  for (const [key, want] of [['ArrowDown', 'a'], ['j', 'a'], ['PageDown', 'a'], ['Home', 'a'], ['ArrowUp', 'd'], ['k', 'd'], ['PageUp', 'd'], ['End', 'd']]) {
    assert.equal(moveFocus(ids, 'gone', key, 2), want, key);
    assert.equal(moveFocus(ids, '', key, 2), want, `${key} from nothing`);
  }
  assert.equal(moveFocus(ids, 'gone', 'x'), 'gone');
  assert.equal(moveFocus(ids, 'b', 'PageDown', 0), 'c', 'a zero page still moves one');
});

test('nextAttention goes worst state first, rotates, and flags low stats', () => {
  const rows = [row('c1'), row('c2', { state: 'offline' }), row('c3', { state: 'dead' }), row('c4', { lowHunger: true }), row('c5', { state: 'stuck' })];
  assert.equal(nextAttention(rows, 'c1'), 'c3');
  assert.equal(nextAttention(rows, 'c3'), 'c5');
  assert.equal(nextAttention(rows, 'c5'), 'c2');
  assert.equal(nextAttention(rows, 'c2'), 'c4');
  assert.equal(nextAttention(rows, 'c4'), 'c3');
  assert.equal(nextAttention([row('c1')], 'c1'), '');
});

test('board: a state change seen live is exact, one the page opened on is not', () => {
  const b = createBoard();
  b.ingest([u('c1'), u('c2', { task: task() })], 10_000);
  let rows = b.rows();
  assert.deepEqual(rows.map(r => [r.state, r.since.exact, r.since.at]), [['idle', false, 10_000], ['working', false, 10_000]]);
  b.ingest([u('c1', { task: task({ id: 't9' }) }), u('c2', { task: task() })], 15_000);
  rows = b.rows();
  assert.deepEqual([rows[0].state, rows[0].since.exact, rows[0].since.at], ['working', true, 15_000]);
  assert.deepEqual([rows[1].state, rows[1].since.exact, rows[1].since.at], ['working', false, 10_000]);
});

test('board: the backlog arriving after the first snapshot fixes the start time', () => {
  const b = createBoard();
  b.ingest([u('c1', { task: task() })], 500_000);
  b.events([ev(3, 'c1', 'task_started', { task: 't1' })], false);
  const r = b.rows()[0];
  assert.equal(r.since.exact, true);
  assert.equal(r.since.at, Date.parse(ev(3).at));
});

test('board: an offline unit takes its time from the offline event', () => {
  const b = createBoard();
  b.events([ev(1, 'c1', 'offline')], false);
  b.ingest([u('c1', { online: false })], 500_000);
  assert.equal(b.rows()[0].since.at, Date.parse(ev(1).at));
});

test('board: a stuck event marks only the task it names, until progress moves', () => {
  const b = createBoard();
  b.ingest([u('c1', { task: task({ progress: 2 }) })], 1000);
  b.events([ev(1, 'c1', 'stuck', { task: 't1', level: 'warn' })], true, 1000);
  assert.equal(b.rows()[0].state, 'stuck');
  assert.equal(b.rows()[0].since.at, Date.parse(ev(1).at));
  b.ingest([u('c1', { task: task({ progress: 2 }) })], 2000);
  assert.equal(b.rows()[0].state, 'stuck');
  b.ingest([u('c1', { task: task({ progress: 3 }) })], 3000);
  assert.equal(b.rows()[0].state, 'working');
  // A new task with a different id was never reported stuck.
  b.events([ev(2, 'c1', 'stuck', { task: 't1', level: 'warn' })], true, 3500);
  b.ingest([u('c1', { task: task({ id: 't2', progress: 0 }) })], 4000);
  assert.equal(b.rows()[0].state, 'working');
});

test('board: a stuck event for another unit or task changes nothing', () => {
  const b = createBoard();
  b.ingest([u('c1', { task: task() }), u('c2', { task: task({ id: 't2' }) })], 1000);
  b.events([ev(1, 'c2', 'stuck', { task: 'old', level: 'warn' })], true, 1000);
  assert.deepEqual(b.rows().map(r => r.state), ['working', 'working']);
});

test('board: summary counts done and failed from the feed window only', () => {
  const b = createBoard({ feedCap: 3 });
  b.ingest([u('c1')], 1000);
  b.events([ev(1, 'c1', 'task_done'), ev(2, 'c1', 'task_done'), ev(3, 'c1', 'task_failed'), ev(4, 'c1', 'task_done')], false);
  const s = b.summary();
  assert.equal(s.events, 3);
  assert.equal(s.done, 2);
  assert.equal(s.failed, 1);
  // The same event delivered by the backlog and the live line counts once.
  b.events([ev(4, 'c1', 'task_done')], true);
  assert.equal(b.summary().done, 2);
});

test('board: the health trail is capped, unknown is a gap, and the fleet trail follows states', () => {
  const b = createBoard({ window: 3 });
  b.ingest([u('c1', { health: 18 }), u('c2', { task: task() })], 1000);
  for (let i = 0; i < 5; i++) b.sample(1000 + i * SAMPLE_MS);
  b.ingest([u('c1', { online: false, health: null }), u('c2', { task: task() })], 11_000);
  b.sample(11_000);
  const r = b.rows();
  assert.deepEqual(r[0].trail, [18, 18, null]);
  assert.equal(r[1].trail.length, 3);
  assert.deepEqual(b.fleetTrail('online'), [2, 2, 1]);
  assert.deepEqual(b.fleetTrail('working'), [1, 1, 1]);
  assert.deepEqual(b.fleetTrail('attention'), [0, 0, 1]);
  assert.ok(WINDOW >= 60);
});

test('board: samples carry their real time and nothing is sampled while the stream is silent', () => {
  const b = createBoard();
  b.sample(500);
  assert.equal(b.times().length, 0, 'nothing to sample before the first snapshot');
  b.ingest([u('c1', { health: 10 })], 1000);
  b.sample(1000);
  b.sample(3000);
  // The stream went quiet: ticks keep firing but the data is old.
  b.sample(3000 + STALE_MS + 1);
  b.sample(60_000);
  assert.deepEqual(b.times(), [1000, 3000]);
  assert.deepEqual(b.rows()[0].trail, [10, 10]);
  // It comes back: a break marks the hole, then real samples follow.
  b.ingest([u('c1', { health: 12 })], 100_000);
  b.sample(100_000);
  assert.deepEqual(b.rows()[0].trail, [10, 10, null, 12]);
  assert.deepEqual(b.times(), [1000, 3000, 3000 + SAMPLE_MS, 100_000]);
  assert.deepEqual(b.fleetTrail('online'), [1, 1, null, 1]);
  assert.ok(100_000 - 3000 > GAP_MS);
  // A tick one interval after the last is the ordinary case: no break.
  b.ingest([u('c1', { health: 12 })], 102_000);
  b.sample(102_000);
  assert.deepEqual(b.rows()[0].trail, [10, 10, null, 12, 12]);
});

test('trailLabels name samples by their own age, not by their count', () => {
  assert.deepEqual(trailLabels([0, 2000, 4000], 3, 4000), ['4s ago', '2s ago', 'now']);
  // After a hole the older samples are minutes away, not seconds.
  assert.deepEqual(trailLabels([0, 60_000, 300_000], 3, 301_000), ['5m ago', '4m ago', '1s ago'].map((x, i) => (i === 2 ? 'now' : x)));
  assert.deepEqual(trailLabels([0, 2000, 4000], 2, 4000), ['2s ago', 'now']);
  // A last sample that is old is not "now".
  assert.deepEqual(trailLabels([0, 2000], 2, 62_000), ['62s ago', '60s ago']);
  assert.deepEqual(trailLabels([], 3, 1000), []);
});

test('board: kinds accumulate and positions come through for known units only', () => {
  const b = createBoard();
  b.ingest([u('c1', { task: task() }), u('c2', { task: task({ template: 'haul' }) }), u('c3')], 1000);
  b.ingest([u('c1'), u('c2', { task: task({ template: 'haul' }) }), u('c3')], 2000);
  assert.deepEqual(b.kinds(), ['gather', 'haul']);
  b.positions([{ id: 'c1', x: 10.6, y: 64, z: -3.2, dim: 0, known: true }, { id: 'c2', x: 0, y: 0, z: 0, dim: 0, known: false }]);
  assert.deepEqual(b.rows().map(r => r.pos), [{ x: 11, y: 64, z: -3, dim: 0 }, null, null]);
  b.clearPositions();
  assert.equal(b.rows()[0].pos, null);
});

test('board: low flags ignore offline and dead units', () => {
  const b = createBoard();
  b.ingest([u('c1', { health: 3, hunger: 2 }), u('c2', { dead: true, health: 0 }), u('c3', { online: false, health: 1, hunger: 1 })], 1000);
  assert.deepEqual(b.rows().map(r => [r.lowHealth, r.lowHunger]), [[true, true], [false, false], [false, false]]);
});

test('board: a second death is not dated by the first one when the snapshot comes first', () => {
  const b = createBoard();
  // Died at +1s, back at +2s, dead again at +5s. Wall clock is the event time.
  b.events([ev(1, 'c1', 'died'), ev(2, 'c1', 'respawned')], false);
  b.ingest([u('c1')], 1_002_000 + 500);
  assert.equal(b.rows()[0].since.at, Date.parse(ev(2).at));
  b.ingest([u('c1', { dead: true, health: 0 })], 1_005_100);
  let r = b.rows()[0];
  assert.equal(r.state, 'dead');
  assert.equal(r.since.at, 1_005_100, 'the earlier death is not this one');
  assert.equal(r.since.exact, true, 'but this page watched it happen');
  // The event follows the snapshot and corrects the time.
  b.events([ev(5, 'c1', 'died')], true, 1_005_200);
  r = b.rows()[0];
  assert.equal(r.since.at, Date.parse(ev(5).at));
  assert.equal(r.state, 'dead');
});

test('board: going offline and finishing a task a second time are dated the same way', () => {
  const b = createBoard();
  b.events([ev(1, 'c1', 'offline'), ev(2, 'c1', 'online'), ev(3, 'c2', 'task_done', { task: 't1' })], false);
  b.ingest([u('c1'), u('c2', { task: task({ id: 't2' }) })], 1_004_000);
  b.ingest([u('c1', { online: false }), u('c2')], 1_006_100);
  let r = b.rows();
  assert.equal(r[0].since.at, 1_006_100, 'not the earlier offline');
  assert.equal(r[1].since.at, 1_006_100, 'not the earlier task_done');
  b.events([ev(6, 'c1', 'offline'), ev(6.5, 'c2', 'task_done', { task: 't2' })], true, 1_006_200);
  r = b.rows();
  assert.equal(r[0].since.at, Date.parse(ev(6).at));
  assert.equal(r[1].since.at, Date.parse(ev(6.5).at));
});

test('board: an open-page floor accepts an older event, because the page missed the start', () => {
  const b = createBoard();
  b.ingest([u('c1', { dead: true, health: 0 })], 1_100_000);
  assert.equal(b.rows()[0].since.exact, false);
  b.events([ev(1, 'c1', 'died')], false, 1_100_001);
  assert.deepEqual([b.rows()[0].since.at, b.rows()[0].since.exact], [Date.parse(ev(1).at), true]);
});

test('board: working time restarts when a unit stops being stuck', () => {
  const b = createBoard();
  b.events([ev(1, 'c1', 'task_started', { task: 't1' })], false);
  b.ingest([u('c1', { task: task({ progress: 2 }) })], 1_001_500);
  assert.equal(b.rows()[0].since.at, Date.parse(ev(1).at));
  b.events([ev(2, 'c1', 'stuck', { task: 't1', level: 'warn' })], true, 1_002_500);
  assert.equal(b.rows()[0].state, 'stuck');
  b.ingest([u('c1', { task: task({ progress: 3 }) })], 1_600_000);
  const r = b.rows()[0];
  assert.equal(r.state, 'working');
  assert.equal(r.since.at, 1_600_000, 'not the task start, ten stuck minutes ago');
  // A later event cannot pull it back to the task start either.
  b.events([ev(3, 'c2', 'online')], true, 1_600_100);
  assert.equal(b.rows()[0].since.at, 1_600_000);
});

test('board: a stuck mark expires when nothing refreshes it', () => {
  const b = createBoard();
  const stuckAt = Date.parse(ev(1).at);
  b.ingest([u('c1', { task: task({ progress: 2 }) })], stuckAt);
  b.events([ev(1, 'c1', 'stuck', { task: 't1', level: 'warn' })], true, stuckAt);
  b.ingest([u('c1', { task: task({ progress: 2 }) })], stuckAt + STUCK_HOLD_MS - 1);
  assert.equal(b.rows()[0].state, 'stuck');
  b.ingest([u('c1', { task: task({ progress: 2 }) })], stuckAt + STUCK_HOLD_MS);
  assert.equal(b.rows()[0].state, 'working');
  assert.equal(b.rows()[0].since.at, stuckAt + STUCK_HOLD_MS);
  // It does not come back by itself.
  b.ingest([u('c1', { task: task({ progress: 2 }) })], stuckAt + STUCK_HOLD_MS + 1000);
  assert.equal(b.rows()[0].state, 'working');
  // A fresh report starts a fresh hold.
  b.events([ev(1000, 'c1', 'stuck', { task: 't1', level: 'warn' })], true, Date.parse(ev(1000).at));
  assert.equal(b.rows()[0].state, 'stuck');
});

test('board: units that left the roster lose their history', () => {
  const b = createBoard();
  b.events([ev(1, 'c1', 'stuck', { task: 't1', level: 'warn' })], false);
  b.ingest([u('c1', { task: task() }), u('c2')], 1_001_000);
  b.sample(1_001_000);
  assert.equal(b.rows().length, 2);
  // Same length, different units.
  b.ingest([u('c3'), u('c4')], 1_003_000);
  b.sample(1_003_000);
  assert.deepEqual(b.rows().map(r => r.id), ['c3', 'c4']);
  assert.deepEqual(b.rows().map(r => r.trail.length), [1, 1]);
  // c1 is back: nothing of its old stuck mark, since time or trail is left.
  b.ingest([u('c1', { task: task() })], 1_005_000);
  const r = b.rows()[0];
  assert.equal(r.state, 'working');
  assert.equal(r.since.exact, false);
  assert.equal(r.trail.length, 0);
});
