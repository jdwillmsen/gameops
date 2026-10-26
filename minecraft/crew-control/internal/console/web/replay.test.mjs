import test from 'node:test';
import assert from 'node:assert/strict';
import {
  buildTracks, indexAt, positionAt, trailOf, frameAt, advance, boundsOf, eventsUpTo, withMs, positionsQuery, eventsQuery,
  fetchAll, summary, clock, MAX_GAP, HOLD,
} from './replay.js';
import { LAB } from './heat.js';

const S = (u, t, x, z, y = 64) => ({ u, t, x, y, z });

test('positionAt returns the recorded values exactly at sample times', () => {
  const rows = [S('a', 0, 10, 20), S('a', 2000, 14, 22), S('a', 4000, 20, 30)];
  const tr = buildTracks(rows).get('a');
  for (const r of rows) assert.deepEqual(positionAt(tr, r.t), { x: r.x, y: r.y, z: r.z });
});

test('positionAt interpolates linearly between samples', () => {
  const tr = buildTracks([S('a', 0, 0, 0), S('a', 2000, 10, -20)]).get('a');
  assert.deepEqual(positionAt(tr, 500), { x: 2.5, y: 64, z: -5 });
  assert.deepEqual(positionAt(tr, 1000), { x: 5, y: 64, z: -10 });
});

test('before the first sample there is no position; after the last it is held, then gone', () => {
  const tr = buildTracks([S('a', 1000, 1, 1), S('a', 3000, 5, 5)]).get('a');
  assert.equal(positionAt(tr, 999), null);
  assert.deepEqual(positionAt(tr, 3000 + HOLD), { x: 5, y: 64, z: 5 });
  assert.equal(positionAt(tr, 3000 + HOLD + 1), null);
});

test('a gap longer than MAX_GAP is not interpolated across', () => {
  const tr = buildTracks([S('a', 0, 0, 0), S('a', MAX_GAP + 1, 100, 100), S('a', MAX_GAP + 2001, 102, 100)]).get('a');
  assert.deepEqual(positionAt(tr, 1000), { x: 0, y: 64, z: 0 });
  assert.equal(positionAt(tr, HOLD + 1), null);
  assert.equal(positionAt(tr, MAX_GAP - 1), null);
  assert.equal(positionAt(tr, MAX_GAP + 1).x, 100);
  const edge = buildTracks([S('a', 0, 0, 0), S('a', MAX_GAP, 90, 0)]).get('a');
  assert.equal(positionAt(edge, MAX_GAP / 2).x, 45);
});

test('indexAt is a floor search with ties on the later equal timestamp', () => {
  const tr = buildTracks([S('a', 0, 0, 0), S('a', 10, 1, 1), S('a', 10, 2, 2), S('a', 20, 3, 3)]).get('a');
  assert.equal(indexAt(tr, -1), -1);
  assert.equal(indexAt(tr, 9), 0);
  assert.equal(indexAt(tr, 10), 2);
  assert.equal(indexAt(tr, 1e9), 3);
});

test('trailOf keeps the window, stops at a gap, and ends at the present position', () => {
  const rows = [S('a', 0, 0, 0), S('a', 20_000, 1, 0), S('a', 40_000, 2, 0), S('a', 60_000, 3, 0)];
  const tr = buildTracks(rows).get('a');
  const trail = trailOf(tr, 50_000, 30_000);
  assert.deepEqual(trail.map(p => p.x), [1, 2, 2.5]);
  const gap = buildTracks([S('a', 0, 0, 0), S('a', MAX_GAP + 1000, 50, 0), S('a', MAX_GAP + 3000, 51, 0)]).get('a');
  assert.deepEqual(trailOf(gap, MAX_GAP + 3000, 10 * MAX_GAP).map(p => p.x), [50, 51, 51]);
  assert.deepEqual(trailOf(tr, -1), []);
});

test('frameAt lists only the units with a position at that instant', () => {
  const tracks = buildTracks([S('a', 0, 1, 1), S('b', 5000, 9, 9), S('a', 2000, 3, 3)]);
  assert.deepEqual(frameAt(tracks, 1000).map(f => f.unit), ['a']);
  assert.deepEqual(frameAt(tracks, 5000).map(f => f.unit).sort(), ['a', 'b']);
});

test('advance scales by speed and clamps to the end', () => {
  assert.deepEqual(advance(1000, 100, 4, 5000), { time: 1400, ended: false });
  assert.deepEqual(advance(4900, 100, 16, 5000), { time: 5000, ended: true });
});

test('boundsOf always covers the lab and grows to fit samples and places', () => {
  const near = boundsOf(buildTracks([S('a', 0, LAB.minX + 1, LAB.minZ + 1)]));
  assert.ok(near.minX < LAB.minX && near.maxZ > LAB.maxZ);
  const far = boundsOf(buildTracks([S('a', 0, 2000, -3000)]), [{ min: { x: 0, z: 0 }, max: { x: 5, z: 5 } }]);
  assert.ok(far.maxX >= 2000 && far.minZ <= -3000 && far.minX <= 0);
});

test('eventsUpTo is synced to the scrubber: newest first, bounded, optionally one unit', () => {
  const ev = [
    { id: 1, unit: 'a', at: '2026-10-26T09:00:00Z' }, { id: 2, unit: 'b', at: '2026-10-26T09:00:10Z' },
    { id: 3, unit: 'a', at: '2026-10-26T09:00:20Z' }, { id: 4, unit: 'a', at: '2026-10-26T09:00:30Z' },
  ].map(withMs);
  const at = Date.parse('2026-10-26T09:00:25Z');
  assert.deepEqual(eventsUpTo(ev, at).map(e => e.id), [3, 2, 1]);
  assert.deepEqual(eventsUpTo(ev, at, 2).map(e => e.id), [3, 2]);
  assert.deepEqual(eventsUpTo(ev, at, 5, 'b').map(e => e.id), [2]);
  assert.deepEqual(eventsUpTo(ev, at - 1e6), []);
  assert.deepEqual(eventsUpTo(ev, Date.parse('2026-10-26T09:00:20Z')).map(e => e.id), [3, 2, 1]);
});

test('queries carry the range, the unit and the cursor', () => {
  const since = Date.UTC(2026, 9, 26, 9), until = Date.UTC(2026, 9, 26, 10);
  assert.equal(positionsQuery({ since, until, unit: 'crew-02', cursor: 'c', limit: 5000 }),
    '/v1/replay/positions?since=2026-10-26T09%3A00%3A00.000Z&until=2026-10-26T10%3A00%3A00.000Z&unit=crew-02&limit=5000&cursor=c');
  assert.equal(eventsQuery({ since, until }),
    '/v1/events?since=2026-10-26T09%3A00%3A00.000Z&until=2026-10-26T10%3A00%3A00.000Z&sort=asc&limit=200');
});

test('fetchAll follows cursors to the end, stops at the cap and reports errors', async () => {
  const pages = { '': { rows: [1, 2], next: 'p2' }, p2: { rows: [3], next: 'p3' }, p3: { rows: [4] } };
  const api = async (_m, path) => ({ ok: true, status: 200, data: pages[path] });
  assert.deepEqual((await fetchAll(api, c => c, 'rows', 10)).rows, [1, 2, 3, 4]);
  const capped = await fetchAll(api, c => c, 'rows', 2);
  assert.deepEqual([capped.rows, capped.truncated], [[1, 2, 3], true]);
  const bad = await fetchAll(async () => ({ ok: false, status: 503, data: { error: 'x' } }), c => c, 'rows', 3);
  assert.deepEqual([bad.error, bad.status], ['x', 503]);
  const stale = await fetchAll(api, c => c, 'rows', 3, () => false);
  assert.equal(stale.aborted, true);
});

test('summary and clock are plain', () => {
  assert.equal(summary(new Map(), [], 0, 1), 'No positions were recorded in this range.');
  const tracks = buildTracks([S('a', 0, 1, 1), S('b', 0, 1, 1)]);
  assert.match(summary(tracks, [1, 2, 3], 0, 1000), /^2 positions from 2 units and 3 events, \d\d:\d\d:\d\d to \d\d:\d\d:\d\d\.$/);
  assert.match(clock(0), /^\d\d:\d\d:\d\d$/);
});

// The budget is under 100 ms a frame for an hour of twenty units. The hard
// bound here is ten times tighter than that, so the measurement is not noise.
test('scrubbing an hour of twenty moving units computes a frame well inside 100 ms', () => {
  const rows = [];
  for (let u = 0; u < 20; u++) {
    for (let i = 0; i < 1800; i++) rows.push(S(`crew-${u}`, i * 2000, 300 + u * 5 + Math.sin(i / 20) * 40, -450 + Math.cos(i / 30) * 30));
  }
  rows.sort((a, b) => a.t - b.t);
  const tracks = buildTracks(rows);
  const frames = 2000;
  const start = performance.now();
  let seen = 0;
  for (let f = 0; f < frames; f++) {
    const t = (f * 7919) % (3600 * 1000);
    seen += frameAt(tracks, t).length;
    for (const tr of tracks.values()) trailOf(tr, t);
  }
  const per = (performance.now() - start) / frames;
  console.log(`replay frame: ${per.toFixed(4)} ms per frame, 20 units x 1800 samples`);
  assert.ok(seen > 0);
  assert.ok(per < 10, `${per} ms per frame`);
});
