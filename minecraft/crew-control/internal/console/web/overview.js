// Pure logic for the fleet overview: no DOM, so node can test it. Relative
// imports keep it loadable from node as well as the browser.
import { LOW, FEED_CAP, mergeEvents, taskLine } from './fleet.js';

export const SAMPLE_MS = 2000;
// 90 samples at 2 s is a three-minute trail.
export const WINDOW = 90;
export const MAX_HEALTH = 20;
// The stream re-sends the units every 15 s even when nothing changed, so
// silence for longer than that plus slack means the data is not live.
export const STALE_MS = 20_000;
// A gap this wide between two samples is drawn as a break, not a line.
export const GAP_MS = 3 * SAMPLE_MS;
// The poller reports a stuck task once and never says when the unit moves
// again, and the units snapshot carries no stuck flag to read it from. Past
// this long without a new report the mark is dropped, so a unit that went on
// working is not shown as stuck for days.
export const STUCK_HOLD_MS = 10 * 60_000;

// Worst first, which is also the order a sort by state shows.
export const STATES = ['dead', 'stuck', 'offline', 'working', 'idle'];
export const STATE_LABEL = { dead: 'Dead', stuck: 'Stuck', offline: 'Offline', working: 'Working', idle: 'Idle' };
const STATE_RANK = Object.fromEntries(STATES.map((s, i) => [s, i]));
export const isState = s => STATES.includes(s);

export const SORTS = ['unit', 'name', 'state', 'health', 'hunger', 'task', 'since'];
export const SORT_LABEL = { unit: '#', name: 'Unit', state: 'State', health: 'Health', hunger: 'Hunger', task: 'Task', since: 'In state' };
// The first click on a column should show the interesting end: the sickest
// and the longest-waiting units, not the healthiest.
export const DEFAULT_DIR = { unit: 'asc', name: 'asc', state: 'asc', health: 'asc', hunger: 'asc', task: 'asc', since: 'desc' };

export const LEVELS = [0, 6, 10, 15];

// A stuck unit still has its task, so it is told apart by the stuck event.
export function stateOf(s, stuck = false) {
  if (!s.online) return 'offline';
  if (s.dead) return 'dead';
  if (!s.task) return 'idle';
  return stuck ? 'stuck' : 'working';
}

export function taskKind(s) {
  if (!s.online || !s.task) return 'none';
  return String(s.task.template || 'task').replace('gather_and_deposit', 'gather');
}

const known = v => typeof v === 'number' && !Number.isNaN(v);

// ---- duration ----

const pad = n => String(n).padStart(2, '0');

export function duration(ms) {
  const s = Math.max(0, Math.floor(ms / 1000));
  if (s < 60) return `${s}s`;
  if (s < 3600) return `${Math.floor(s / 60)}m ${pad(s % 60)}s`;
  if (s < 86400) return `${Math.floor(s / 3600)}h ${pad(Math.floor((s % 3600) / 60))}m`;
  return `${Math.floor(s / 86400)}d ${pad(Math.floor((s % 86400) / 3600))}h`;
}

// A time that was only measured from when this page opened is a floor, not
// the truth, and says so.
export function sinceLabel(since, now) {
  if (!since) return '';
  return (since.exact ? '' : '\u2265') + duration(now - since.at);
}

// ---- sorting ----

// Unknown numbers sort last in both directions: a unit that reports nothing is
// never the "lowest" health.
export function sortRows(rows, key, dir = 'asc') {
  const sign = dir === 'desc' ? -1 : 1;
  const val = {
    unit: r => r.index,
    name: r => r.name.toLowerCase(),
    state: r => STATE_RANK[r.state],
    health: r => (known(r.health) ? r.health : null),
    hunger: r => (known(r.hunger) ? r.hunger : null),
    task: r => (r.kind === 'none' ? null : r.taskText.toLowerCase()),
    since: r => (r.since ? r.since.at : null),
  }[key] || (r => r.index);
  // since sorts on the age, so ascending means shortest first.
  const flip = key === 'since' ? -1 : 1;
  return [...rows].sort((a, b) => {
    const x = val(a), y = val(b);
    if (x === null || y === null) return x === y ? a.index - b.index : x === null ? 1 : -1;
    if (x === y) return a.index - b.index;
    return (x < y ? -1 : 1) * sign * flip;
  });
}

// ---- filtering ----

export const emptyFilter = () => ({ q: '', states: [], kind: '', hp: 0, food: 0 });

export const isFiltered = f => !!(f.q || f.states.length || f.kind || f.hp || f.food);

export function filterRows(rows, f) {
  const q = f.q.trim().toLowerCase();
  return rows.filter(r => {
    if (q && !`${r.name} ${r.id} ${r.taskText} ${STATE_LABEL[r.state]}`.toLowerCase().includes(q)) return false;
    if (f.states.length && !f.states.includes(r.state)) return false;
    if (f.kind && r.kind !== f.kind) return false;
    // A unit that reports no health is not "low", so it stays out of the cut.
    if (f.hp && !(known(r.health) && r.health <= f.hp)) return false;
    if (f.food && !(known(r.hunger) && r.hunger <= f.food)) return false;
    return true;
  });
}

// ---- summary ----

export function summarize(rows, feed = []) {
  const c = { total: rows.length, online: 0, working: 0, idle: 0, stuck: 0, dead: 0, offline: 0, attention: 0, done: 0, failed: 0, events: feed.length, since: feed.length ? feed[0].at : '' };
  for (const r of rows) {
    c[r.state]++;
    if (r.state !== 'offline') c.online++;
    if (r.state === 'stuck' || r.state === 'dead' || r.state === 'offline') c.attention++;
  }
  for (const e of feed) {
    if (e.kind === 'task_done') c.done++;
    else if (e.kind === 'task_failed') c.failed++;
  }
  return c;
}

// ---- the address bar ----

const pick = (v, list, d) => (list.includes(v) ? v : d);
const level = v => pick(Number(v), LEVELS, 0);

export function parseParams(search) {
  const q = new URLSearchParams(search);
  const sort = pick(q.get('sort'), SORTS, 'unit');
  const dir = pick(q.get('dir'), ['asc', 'desc'], DEFAULT_DIR[sort]);
  const states = (q.get('st') || '').split(',').filter((s, i, a) => isState(s) && a.indexOf(s) === i);
  return {
    sort,
    dir,
    filter: { q: (q.get('q') || '').slice(0, 40), states, kind: (q.get('task') || '').slice(0, 30), hp: level(q.get('hp')), food: level(q.get('food')) },
  };
}

// Only what differs from the defaults is written, so a plain fleet link stays short.
export function formatParams({ sort, dir, filter }) {
  const q = new URLSearchParams();
  if (sort !== 'unit' || dir !== DEFAULT_DIR.unit) q.set('sort', sort);
  if (dir !== DEFAULT_DIR[sort]) q.set('dir', dir);
  if (filter.q) q.set('q', filter.q);
  if (filter.states.length) q.set('st', filter.states.join(','));
  if (filter.kind) q.set('task', filter.kind);
  if (filter.hp) q.set('hp', String(filter.hp));
  if (filter.food) q.set('food', String(filter.food));
  return q.toString();
}

// ---- keyboard ----

export const needsAttention = r => r.state === 'dead' || r.state === 'stuck' || r.state === 'offline' || r.lowHealth || r.lowHunger;

// The next row after cur that needs a look, worst state first; '' when none.
export function nextAttention(rows, cur) {
  const bad = sortRows(rows.filter(needsAttention), 'state');
  if (!bad.length) return '';
  const i = bad.findIndex(r => r.id === cur);
  return bad[(i + 1) % bad.length].id;
}

// Moves among the ids on screen. It stops at the ends: a table is not a ring,
// and wrapping past the last row hides that the list ended. From a row that is
// not on screen (filtered away, say) there is nothing to step from, so forward
// keys start at the top and backward keys at the bottom.
export function moveFocus(ids, cur, key, page = 8) {
  if (!ids.length) return '';
  const last = ids.length - 1;
  if (key === 'Home') return ids[0];
  if (key === 'End') return ids[last];
  const span = Math.max(1, page);
  const step = { ArrowDown: 1, j: 1, PageDown: span, ArrowUp: -1, k: -1, PageUp: -span }[key];
  if (step === undefined) return cur;
  const i = ids.indexOf(cur);
  if (i < 0) return ids[step > 0 ? 0 : last];
  return ids[Math.min(last, Math.max(0, i + step))];
}

// Names the last n samples by how long ago they were taken, from their own
// timestamps: a throttled tab or a stream outage spaces them unevenly.
export function trailLabels(times, n, now) {
  const at = times.slice(-n);
  return at.map((t, i) => {
    const s = Math.max(0, Math.round((now - t) / 1000));
    if (i === at.length - 1 && s * 1000 <= SAMPLE_MS * 1.5) return 'now';
    return s < 90 ? `${s}s ago` : `${Math.round(s / 60)}m ago`;
  });
}

// ---- the board: what the stream cannot tell on its own ----

const push = (arr, v, cap) => {
  arr.push(v);
  if (arr.length > cap) arr.shift();
};

// Holds what the console learns over time: how long each unit has been in its
// state, which tasks the feed called stuck, a short trail of health, and the
// feed window the done/failed counts come from.
export function createBoard({ window = WINDOW, feedCap = FEED_CAP } = {}) {
  let units = [];
  let feed = [];
  let freshAt = -Infinity;
  const stuckTask = new Map();
  const stuckProgress = new Map();
  const stuckAt = new Map();
  const lastEvent = new Map();
  // Per unit: { state, at, src, floor }. src says where `at` came from: an
  // 'event' from the feed, 'seen' when this page watched the change but no
  // event has confirmed it yet, 'open' when the page opened on the state and
  // only knows it has lasted at least this long. floor is the earliest an
  // event may date the entry: the page saw the previous state at that time, so
  // an older event belongs to an earlier visit to this one.
  const since = new Map();
  const trail = new Map();
  // One timeline for every trail; a unit that joined late holds the tail of it.
  const times = [];
  const fleet = { online: [], working: [], idle: [], attention: [] };
  const kinds = new Set();
  let positions = new Map();

  // The feed names a stuck task but never says when it moves again, so a stuck
  // mark holds until the task changes, its progress does, or STUCK_HOLD_MS passes.
  function stuckNow(s, now) {
    const want = stuckTask.get(s.id);
    if (!want) return false;
    const at = Date.parse(stuckAt.get(s.id));
    if (!s.task || s.task.id !== want || (!Number.isNaN(at) && now - at >= STUCK_HOLD_MS)) {
      stuckTask.delete(s.id);
      stuckProgress.delete(s.id);
      stuckAt.delete(s.id);
      return false;
    }
    if (!stuckProgress.has(s.id)) stuckProgress.set(s.id, s.task.progress);
    else if (stuckProgress.get(s.id) !== s.task.progress) {
      stuckTask.delete(s.id);
      stuckProgress.delete(s.id);
      stuckAt.delete(s.id);
      return false;
    }
    return true;
  }

  // When the feed says the unit entered this state, else null. Events dated
  // before floor are ignored: the stream may deliver a snapshot ahead of its
  // event, and the newest event of that kind is then an earlier visit.
  function enteredAt(s, state, floor) {
    const ev = lastEvent.get(s.id) || {};
    const t = e => {
      const at = e ? Date.parse(e.at) : NaN;
      return at >= floor ? at : NaN;
    };
    let at = NaN;
    if (state === 'offline') at = t(ev.offline);
    else if (state === 'dead') at = t(ev.died);
    else if (state === 'stuck') at = Date.parse(stuckAt.get(s.id));
    else if (state === 'working') {
      const e = ev.task_started;
      at = e && s.task && e.task === s.task.id ? t(e) : NaN;
    } else {
      const ended = ['task_done', 'task_failed', 'task_cancelled', 'task_ended', 'respawned', 'online'].map(k => t(ev[k])).filter(n => !Number.isNaN(n));
      if (ended.length) at = Math.max(...ended);
    }
    return Number.isNaN(at) ? null : at;
  }

  // Departed units must not keep rows, trails or marks alive.
  function prune(list) {
    const live = new Set(list.map(s => s.id));
    for (const m of [since, trail, lastEvent, stuckTask, stuckProgress, stuckAt]) {
      for (const id of m.keys()) if (!live.has(id)) m.delete(id);
    }
  }

  return {
    ingest(list, now = Date.now()) {
      units = list;
      freshAt = now;
      prune(list);
      for (const s of list) {
        const state = stateOf(s, stuckNow(s, now));
        const k = taskKind(s);
        if (k !== 'none') kinds.add(k);
        const cur = since.get(s.id);
        if (!cur || cur.state !== state) {
          const floor = cur ? cur.at : 0;
          const at = enteredAt(s, state, floor);
          // A change this page watched happen is exact; one it opened on is not.
          // Leaving stuck restarts the clock: the task's start is older than
          // the stuck spell, so the feed cannot date the working time.
          const src = at !== null ? 'event' : cur ? 'seen' : 'open';
          since.set(s.id, { state, at: at ?? now, src, floor });
        }
      }
    },

    // live is false for the backlog, which replaces the window.
    events(incoming, live, now = Date.now()) {
      const r = mergeEvents(live ? feed : [], incoming, feedCap);
      feed = r.feed;
      for (const e of r.added) {
        const m = lastEvent.get(e.unit) || {};
        if (!m[e.kind] || e.seq >= m[e.kind].seq) m[e.kind] = e;
        lastEvent.set(e.unit, m);
        if (e.kind === 'stuck' && e.task) {
          stuckTask.set(e.unit, e.task);
          stuckProgress.delete(e.unit);
          stuckAt.set(e.unit, e.at);
        }
      }
      // Units that arrived before their backlog, or ahead of their event, get
      // a better start time now.
      for (const s of units) {
        const cur = since.get(s.id);
        if (!cur || cur.src === 'event') continue;
        const at = enteredAt(s, cur.state, cur.floor);
        if (at !== null) since.set(s.id, { ...cur, at, src: 'event' });
      }
      if (r.added.some(e => e.kind === 'stuck')) this.ingest(units, now);
    },

    positions(list) {
      positions = new Map(list.filter(p => p.known).map(p => [p.id, p]));
    },
    clearPositions() {
      positions = new Map();
    },

    // One sample per unit, null while a unit reports nothing, so an outage
    // shows as a gap instead of a flat line. Nothing is sampled while the
    // stream is silent: a repeated old value would pass for a live one.
    sample(now = Date.now()) {
      if (!units.length || now - freshAt > STALE_MS) return;
      // The clock jumped or the tab was throttled: break the line between the
      // samples either side instead of drawing a slope across the hole.
      if (times.length && now - times[times.length - 1] > GAP_MS) {
        for (const h of trail.values()) push(h, null, window);
        for (const k of Object.keys(fleet)) push(fleet[k], null, window);
        push(times, times[times.length - 1] + SAMPLE_MS, window);
      }
      const c = { online: 0, working: 0, idle: 0, attention: 0 };
      for (const s of units) {
        const state = since.get(s.id)?.state ?? stateOf(s);
        let h = trail.get(s.id);
        if (!h) trail.set(s.id, (h = []));
        push(h, state !== 'offline' && known(s.health) ? s.health : null, window);
        if (state !== 'offline') c.online++;
        if (state === 'working') c.working++;
        if (state === 'idle') c.idle++;
        if (state === 'stuck' || state === 'dead' || state === 'offline') c.attention++;
      }
      for (const k of Object.keys(c)) push(fleet[k], c[k], window);
      push(times, now, window);
    },

    rows() {
      return units.map((s, index) => {
        const st = since.get(s.id);
        const state = st ? st.state : stateOf(s);
        const p = positions.get(s.id);
        return {
          id: s.id,
          name: s.name || s.id,
          index: index + 1,
          state,
          since: st ? { at: st.at, exact: st.src !== 'open' } : null,
          kind: taskKind(s),
          taskText: taskLine(s),
          health: known(s.health) ? s.health : null,
          hunger: known(s.hunger) ? s.hunger : null,
          lowHealth: s.online && !s.dead && known(s.health) && s.health <= LOW,
          lowHunger: s.online && known(s.hunger) && s.hunger <= LOW,
          pos: p ? { x: Math.round(p.x), y: Math.round(p.y), z: Math.round(p.z), dim: p.dim } : null,
          trail: trail.get(s.id) || [],
        };
      });
    },

    summary() {
      return summarize(this.rows(), feed);
    },
    fleetTrail: k => fleet[k],
    times: () => times,
    kinds: () => [...kinds].sort(),
    feedSize: () => feed.length,
  };
}
