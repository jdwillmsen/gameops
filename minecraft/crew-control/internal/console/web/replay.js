// Logic for the replay view: turning recorded position samples into a
// position at any instant, and playback arithmetic. replay-ui.js paints;
// nothing here touches the DOM.
import { LAB, fit } from './heat.js';

export { fit };

export const SPEEDS = [1, 4, 16];
export const WINDOWS = [
  { id: '10m', label: 'last 10 minutes', ms: 10 * 60_000 },
  { id: '1h', label: 'last hour', ms: 60 * 60_000 },
  { id: '6h', label: 'last 6 hours', ms: 6 * 60 * 60_000 },
];
export const windowOf = id => WINDOWS.find(w => w.id === id) || WINDOWS[0];

// The recorder stores a standing unit every 30 s, so a longer gap means the
// unit was offline or dead, not still. Interpolating across it would drag a
// unit in a straight line through walls to where it came back.
export const MAX_GAP = 90_000;
// How long the last known position is still shown after a sample, so a unit
// that goes offline does not vanish between two keepalives.
export const HOLD = 45_000;
export const TRAIL_MS = 60_000;

// Page caps keep a runaway range from filling memory; the view says so when
// one bites.
export const MAX_POSITION_PAGES = 40;
export const MAX_EVENT_PAGES = 25;

// samples arrive oldest first across the fleet; per unit they stay sorted.
export function buildTracks(samples) {
  const tracks = new Map();
  for (const s of samples) {
    let t = tracks.get(s.u);
    if (!t) tracks.set(s.u, t = { unit: s.u, t: [], x: [], y: [], z: [] });
    t.t.push(s.t); t.x.push(s.x); t.y.push(s.y); t.z.push(s.z);
  }
  return tracks;
}

// Index of the last sample at or before time, or -1.
export function indexAt(track, time) {
  const a = track.t;
  let lo = 0, hi = a.length - 1, ans = -1;
  while (lo <= hi) {
    const mid = (lo + hi) >> 1;
    if (a[mid] <= time) { ans = mid; lo = mid + 1; } else hi = mid - 1;
  }
  return ans;
}

// Where the unit stood at time, or null when nothing was recorded near it.
export function positionAt(track, time) {
  const i = indexAt(track, time);
  if (i < 0) return null;
  const { t, x, y, z } = track;
  if (i === t.length - 1 || t[i + 1] - t[i] > MAX_GAP) {
    return time - t[i] <= HOLD ? { x: x[i], y: y[i], z: z[i] } : null;
  }
  const f = (time - t[i]) / (t[i + 1] - t[i]);
  return { x: x[i] + (x[i + 1] - x[i]) * f, y: y[i] + (y[i + 1] - y[i]) * f, z: z[i] + (z[i + 1] - z[i]) * f };
}

// The recorded points in the trail window ending at time, oldest first, then
// the interpolated present so the line meets the dot.
export function trailOf(track, time, span = TRAIL_MS) {
  const end = indexAt(track, time);
  if (end < 0) return [];
  let start = end;
  while (start > 0 && track.t[start - 1] >= time - span && track.t[start] - track.t[start - 1] <= MAX_GAP) start--;
  const out = [];
  for (let i = start; i <= end; i++) if (track.t[i] >= time - span) out.push({ x: track.x[i], z: track.z[i] });
  const now = positionAt(track, time);
  if (now) out.push({ x: now.x, z: now.z });
  return out;
}

// Every unit that has a position at time.
export function frameAt(tracks, time) {
  const out = [];
  for (const tr of tracks.values()) {
    const p = positionAt(tr, time);
    if (p) out.push({ unit: tr.unit, ...p });
  }
  return out;
}

// Playback clock: wall time passed, scaled by speed, clamped to the range.
export function advance(time, wallMs, speed, end) {
  const next = time + wallMs * speed;
  return next >= end ? { time: end, ended: true } : { time: next, ended: false };
}

export function boundsOf(tracks, boxes = []) {
  let { minX, maxX, minZ, maxZ } = LAB;
  for (const tr of tracks.values()) {
    for (let i = 0; i < tr.t.length; i++) {
      if (tr.x[i] < minX) minX = tr.x[i]; else if (tr.x[i] > maxX) maxX = tr.x[i];
      if (tr.z[i] < minZ) minZ = tr.z[i]; else if (tr.z[i] > maxZ) maxZ = tr.z[i];
    }
  }
  for (const b of boxes) {
    minX = Math.min(minX, b.min.x); maxX = Math.max(maxX, b.max.x);
    minZ = Math.min(minZ, b.min.z); maxZ = Math.max(maxZ, b.max.z);
  }
  const pad = 8;
  return { minX: minX - pad, maxX: maxX + pad, minZ: minZ - pad, maxZ: maxZ + pad };
}

// Events are sorted oldest first (the API's ascending order). The newest n
// at or before time, newest first, so the list reads like the live feed.
export function eventsUpTo(events, time, n = 12, unit = '') {
  let lo = 0, hi = events.length;
  while (lo < hi) {
    const mid = (lo + hi) >> 1;
    if (events[mid].ms <= time) lo = mid + 1; else hi = mid;
  }
  const out = [];
  for (let i = lo - 1; i >= 0 && out.length < n; i--) {
    if (!unit || events[i].unit === unit) out.push(events[i]);
  }
  return out;
}

export const withMs = e => ({ ...e, ms: Date.parse(e.at) });

export function positionsQuery({ since, until, unit, cursor, limit }) {
  const q = new URLSearchParams({ since: new Date(since).toISOString(), until: new Date(until).toISOString() });
  if (unit) q.set('unit', unit);
  if (limit) q.set('limit', String(limit));
  if (cursor) q.set('cursor', cursor);
  return `/v1/replay/positions?${q}`;
}

export function eventsQuery({ since, until, unit, cursor }) {
  const q = new URLSearchParams({ since: new Date(since).toISOString(), until: new Date(until).toISOString(), sort: 'asc', limit: '200' });
  if (unit) q.set('unit', unit);
  if (cursor) q.set('cursor', cursor);
  return `/v1/events?${q}`;
}

// Walks a keyset-paged route to its end or the page cap. api resolves to
// { ok, status, data }; key names the array in the answer.
export async function fetchAll(api, build, key, maxPages, isCurrent = () => true) {
  const rows = [];
  let cursor = '';
  for (let page = 0; page < maxPages; page++) {
    const r = await api('GET', build(cursor), undefined, { cache: 'no-store' });
    if (!isCurrent()) return { rows, aborted: true };
    if (!r.ok || !r.data) return { rows, status: r.status, error: (r.data && r.data.error) || `Failed (${r.status}).` };
    rows.push(...(r.data[key] || []));
    if (!r.data.next) return { rows };
    cursor = r.data.next;
  }
  return { rows, truncated: true };
}

const pad2 = n => String(n).padStart(2, '0');
export const clock = ms => { const d = new Date(ms); return `${pad2(d.getHours())}:${pad2(d.getMinutes())}:${pad2(d.getSeconds())}`; };

export function summary(tracks, events, from, to) {
  let n = 0;
  for (const t of tracks.values()) n += t.t.length;
  if (!n) return 'No positions were recorded in this range.';
  return `${n} positions from ${tracks.size} ${tracks.size === 1 ? 'unit' : 'units'} and ${events.length} events, ${clock(from)} to ${clock(to)}.`;
}
