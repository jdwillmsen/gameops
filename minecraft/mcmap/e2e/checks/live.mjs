// What moves: the stream, the heads, Follow, and how faces sit on plates.

import { Fail, skip, sleep, until } from '../lib/check.mjs';
import { openContext, openMap } from '../lib/browser.mjs';
import { goToSomething } from './map.mjs';

// The stream a page opened when it loaded, left alone, has to have stayed
// open. The gateway cuts a connection that says nothing for 30 s, so a
// stream that lasts minutes with frames arriving is one whose keepalives
// and frames are getting through it. watcher is a page nothing else uses.
export async function liveStream({ watcher, cfg }) {
  const live = await watcher.evaluate(() => window.mcmap.live());
  if (!live) return skip('the live layer is switched off');
  const read = () => watcher.evaluate(() => {
    const s = window.__probe.streams.reduce((a, b) => (!a || b.frames > a.frames ? b : a), null);
    if (!s) return null;
    const now = performance.now();
    return {
      open: s.opened.length > 0 ? (now - s.opened[0]) / 1000 : 0, connected: s.state() === 1, frames: s.frames, gap: s.gap / 1000,
      silent: (now - (s.last ?? now)) / 1000, most: s.most || 0, errors: s.errors.length, streams: window.__probe.streams.length,
    };
  });
  // The rest of the run was the wait; this is only what is left of it.
  const got = await until(async () => { const s = await read(); return s && (s.open >= cfg.streamSeconds || s.errors > 0) ? s : null; }, { timeoutMs: (cfg.streamSeconds + 30) * 1000, everyMs: 1000 }) || await read();
  if (!got) throw new Fail('the page opened no live stream');
  const measures = { seconds: Math.round(got.open), frames: got.frames, longest_gap_seconds: Math.round(Math.max(got.gap, got.silent) * 10) / 10, reconnects: got.errors, streams: got.streams, most_entities: got.most };
  if (got.errors > 0) throw new Fail('the live stream was cut and had to reconnect', measures);
  if (!got.connected || got.open < cfg.streamSeconds) throw new Fail('the live stream did not stay open', measures);
  if (got.frames === 0) throw new Fail('the live stream sent no frame at all', measures);
  // With nobody and nothing in the dimension the map has nothing new to
  // send, and sends only what keeps the connection: then the connection
  // outliving the gateway's 30 s, which it has, is the whole of the check.
  if (got.most === 0) return { measures };
  // The pack samples every second or two; several missed in a row is a
  // stream that is open and saying nothing.
  if (Math.max(got.gap, got.silent) > cfg.streamGapSeconds) throw new Fail('the live stream went quiet for longer than a few samples', measures);
  return { measures };
}

// A player with a head listed is drawn with it. This is the state a script
// error once hid behind: it only arose with a head in the list.
export async function playerHeads({ page }) {
  const there = await goToSomething(page);
  if (there.players === 0) return skip('nobody is online in this dimension', { players: 0 });
  const got = await until(() => page.evaluate(() => {
    const P = window.__probe;
    const listed = Object.keys(window.mcmap.icons.listing().heads || {}).length;
    let composed = 0;
    for (const record of P.composedList()) if (/\/api\/icons\/head$/.test(new URL(P.origin.get(record.image)).pathname)) composed += 1;
    return listed === 0 || composed > 0 ? { listed, composed } : null;
  }), { timeoutMs: 15_000 }) || { listed: -1, composed: 0 };
  const measures = { players: there.players, heads_listed: Math.max(got.listed, 0), heads_drawn: got.composed };
  if (got.listed === 0) return skip('players are online and none has a head listed', measures);
  if (got.composed === 0) throw new Fail('a head is listed and no player is drawn with one', measures);
  return { measures };
}

// In the page: turns Follow on for one entity and watches it. Answers
// numbers only.
const follow = async ({ kind, seconds, tolerance }) => {
  const P = window.__probe;
  const app = window.mcmap;
  const frame = P.frame || {};
  const heads = app.icons.listing().heads || {};
  const players = frame.players || [];
  const mobs = frame.mobs || [];
  let entity;
  if (kind === 'player') {
    entity = players.find((p) => Object.hasOwn(heads, String(p.n || '').toLowerCase())) || players[0];
  } else {
    // The nearest to the first player is the likeliest to stay within
    // what the pack lists; with nobody online, any.
    const by = players[0];
    entity = by ? [...mobs].sort((a, b) => Math.hypot(a.x - by.x, a.z - by.z) - Math.hypot(b.x - by.x, b.z - by.z))[0] : mobs[0];
  }
  if (!entity) return { none: true };
  const key = `${kind === 'player' ? 'p' : 'm'}:${entity.i}`;
  const wait = (ms) => new Promise((done) => setTimeout(done, ms));
  const map = app.map;
  map.setView([entity.z, entity.x], Math.max(map.getZoom(), 0), { animate: false });
  if (!app.inspect.open({ kind, id: String(entity.i), type: entity.t || '', name: entity.n || '', x: entity.x, y: entity.y, z: entity.z, dimension: app.dimension() })) return { unopened: true };
  await wait(400);
  const button = document.getElementById('inspect-follow');
  button.click();
  await wait(1500);

  const halo = () => P.layer((o, l) => o.interactive === false && o.fill === false && l.getRadius);
  // Where the map's own bounds stop it short of the place asked for, it
  // is not centring that failed.
  const clamped = (latlng, centre) => {
    if (!map._limitCenter || !map.options.maxBounds) return false;
    const want = map.project(latlng, map.getZoom()).subtract([centre.x - centre.box.x, centre.y - centre.box.y]);
    for (const [dx, dy] of [[0, 0], [4, 0], [-4, 0], [0, 4], [0, -4]]) {
      const near = want.add([dx, dy]);
      const kept = map.project(map._limitCenter(map.unproject(near, map.getZoom()), map.getZoom(), map.options.maxBounds), map.getZoom());
      if (Math.hypot(kept.x - near.x, kept.y - near.y) > 0.01) return true;
    }
    return false;
  };
  const out = { samples: 0, measured: 0, worst: 0, over: 0, lost: 0, lied: 0, frames: 0, absent: 0, longestAbsent: 0, moved: 0, offs: [], behind: 0, worstBehind: 0, placed: 0 };
  // How far it went between the last two samples it was in, in blocks.
  let step = 0;
  let seen = P.frame;
  let run = 0;
  let last = null;
  const end = performance.now() + seconds * 1000;
  while (performance.now() < end) {
    await new Promise((done) => requestAnimationFrame(done));
    out.samples += 1;
    if (P.frame !== seen) {
      seen = P.frame;
      out.frames += 1;
      const list = (kind === 'player' ? seen.players : seen.mobs) || [];
      const now = list.find((e) => String(e.i) === String(entity.i));
      if (now) {
        run = 0;
        step = last ? Math.hypot(now.x - last.x, now.z - last.z) : 0;
        if (step > 0.01) out.moved += 1;
        last = now;
      } else {
        out.absent += 1;
        run += 1;
        out.longestAbsent = Math.max(out.longestAbsent, run);
      }
    }
    const on = app.inspect.following() === key;
    if (!on) out.lost += 1;
    if ((button.getAttribute('aria-pressed') === 'true') !== on) out.lied += 1;
    const ring = halo();
    if (!on || !ring || map._animatingZoom) continue;
    const centre = P.centre();
    if (clamped(ring.getLatLng(), centre)) continue;
    const at = P.screen(ring.getLatLng());
    const off = Math.hypot(at.x - centre.x, at.y - centre.y);
    // The mark could sit in the middle and be on nothing. Where the
    // stream last put the entity has to be in the middle too, give or
    // take what it travels between samples, which the page draws it
    // gliding through.
    if (last && run === 0) {
      const truly = P.screen([last.z, last.x]);
      const away = Math.hypot(truly.x - centre.x, truly.y - centre.y);
      out.placed += 1;
      out.worstBehind = Math.max(out.worstBehind, away);
      if (away > 2 * step * 2 ** map.getZoom() + tolerance + 8) out.behind += 1;
    }
    out.measured += 1;
    out.offs.push(off);
    out.worst = Math.max(out.worst, off);
    if (off > tolerance) out.over += 1;
  }
  out.offs.sort((a, b) => a - b);
  out.p95 = out.offs.length ? out.offs[Math.floor(out.offs.length * 0.95)] : 0;
  delete out.offs;
  // Left as it was found.
  if (app.inspect.following() === key) button.click();
  app.inspect.shut();
  return out;
};

async function followCheck({ page, cfg }, kind, seconds) {
  const there = await goToSomething(page);
  if ((kind === 'player' ? there.players : there.mobs) === 0) return skip(kind === 'player' ? 'nobody is online in this dimension' : 'no mob is loaded in this dimension');
  await sleep(500);
  const got = await page.evaluate(follow, { kind, seconds, tolerance: cfg.followTolerancePx });
  if (got.none) return skip('whoever was here has gone');
  if (got.unopened) throw new Fail('the card would not open on a live entity');
  const round = (n) => Math.round(n * 100) / 100;
  const measures = {
    seconds, samples: got.samples, measured: got.measured, worst_pixels: round(got.worst), p95_pixels: round(got.p95), over_tolerance: got.over,
    worst_pixels_from_where_it_is: round(got.worstBehind), samples_left_behind: got.behind,
    frames: got.frames, frames_without_it: got.absent, longest_run_without_it: got.longestAbsent, frames_it_moved: got.moved, follow_off_samples: got.lost,
  };
  // Gone for good is the world's doing: a mob that despawned, a player
  // who left. Gone for a sample or two is the stream's, and Follow has
  // to outlast that.
  const gone = got.longestAbsent * 2 > got.frames || got.longestAbsent >= 8;
  if (got.lost > 0 && gone) return skip('what was followed left the world', measures);
  if (got.lost > 0) throw new Fail('Follow went off by itself', measures);
  if (got.lied > 0) throw new Fail('the Follow button did not say what Follow was', measures);
  if (got.measured === 0) throw new Fail('nothing marked what was followed, so there was nothing to measure', measures);
  if (got.over > 0) throw new Fail('what was followed was not kept in the middle of the map', measures);
  // One sample in ten is room for a frame drawn between a sample's
  // arrival and the map's move; more is a map following something else.
  if (got.behind * 10 > got.placed) throw new Fail('the middle of the map was not where what was followed is', measures);
  return { measures };
}

export const followPlayer = (t) => followCheck(t, 'player', t.cfg.followPlayerSeconds);
export const followMob = (t) => followCheck(t, 'mob', t.cfg.followMobSeconds);

// In the page: has the page compose a marker for every mob it has a face
// for, and measures where each face was put on its plate. Offsets are in
// the canvas's own pixels, which are the screen's.
const measureFaces = async () => {
  const P = window.__probe;
  const icons = window.mcmap.icons;
  const wait = (ms) => new Promise((done) => setTimeout(done, ms));
  for (let n = 0; n < 50 && !icons.listing().pictures.version; n += 1) await wait(100);
  const listing = icons.listing();
  const keys = [];
  for (const key of listing.pictures.keys) {
    if (key.startsWith('face/')) keys.push(`mob/${key.slice(5)}`);
    else if (key.startsWith('villager/')) keys.push(key);
  }
  if (keys.length === 0) return { faces: 0 };
  // Close enough in that markers are pictures: from far out the page
  // draws dots and composes nothing.
  window.mcmap.map.setZoom(Math.max(window.mcmap.map.getZoom(), 0), { animate: false });
  // A picture is fetched when first asked for and drawn once it has come.
  const sprites = new Map();
  for (let turn = 0; turn < 60 && sprites.size < keys.length; turn += 1) {
    for (const key of keys) {
      if (sprites.has(key)) continue;
      const made = icons.plate(key, '#ffffff');
      if (made) sprites.set(key, made);
    }
    await wait(100);
  }
  const inkOf = (image) => {
    P.quiet = true;
    try {
      const scratch = document.createElement('canvas');
      scratch.width = image.width;
      scratch.height = image.height;
      const ctx = scratch.getContext('2d', { willReadFrequently: true });
      ctx.drawImage(image, 0, 0);
      const { data } = ctx.getImageData(0, 0, image.width, image.height);
      let left = image.width; let right = -1; let top = image.height; let bottom = -1;
      for (let at = 3; at < data.length; at += 4) {
        if (data[at] === 0) continue;
        const x = ((at - 3) / 4) % image.width;
        const y = Math.floor((at - 3) / 4 / image.width);
        left = Math.min(left, x); right = Math.max(right, x); top = Math.min(top, y); bottom = Math.max(bottom, y);
      }
      return right < 0 ? { x: 0, y: 0, w: image.width, h: image.height } : { x: left, y: top, w: right - left + 1, h: bottom - top + 1 };
    } finally { P.quiet = false; }
  };
  const out = { faces: 0, unmade: keys.length - sprites.size, eggs: 0, boxed: 0, wrong: [], offsets: {} };
  for (const [key, sprite] of sprites) {
    const record = P.composed.get(sprite);
    if (!record) { out.unmade += 1; continue; }
    const from = /\/api\/icons\/picture\/([a-z0-9_]+\/[a-z0-9_]+)$/.exec(new URL(P.origin.get(record.image)).pathname);
    // Drawn as its spawn egg, which has no head to be off the middle.
    if (!from) { out.eggs += 1; continue; }
    const ink = inkOf(record.image);
    const box = listing.pictures.boxes[from[1]];
    const served = Array.isArray(box) && box.length === 4;
    if (served) out.boxed += 1;
    // The head as the page takes it: where the server says, or what is
    // drawn where it does not.
    const head = served ? { x: box[0], y: box[1], w: box[2], h: box[3] } : ink;
    const per = { x: record.w / record.image.width, y: record.h / record.image.height };
    const middle = record.side / 2;
    const axis = (from0, span, inkFrom, inkSpan, placed, scale) => {
      const off = placed + (from0 + span / 2) * scale - middle;
      // A face all of which fits is kept inside its box, and that may
      // hold its head off the middle: but only ever towards the side
      // with more room, so that what is drawn sits flush on the other.
      const before = placed + inkFrom * scale;
      const after = record.side - (placed + (inkFrom + inkSpan) * scale);
      const towards = off < 0 ? before : after;
      const away = off < 0 ? after : before;
      return { off, inkOff: placed + (inkFrom + inkSpan / 2) * scale - middle, held: away <= towards + 1 };
    };
    const x = axis(head.x, head.w, ink.x, ink.w, record.x, per.x);
    const y = axis(head.y, head.h, ink.y, ink.h, record.y, per.y);
    out.faces += 1;
    out.offsets[from[1]] = { x: x.off, y: y.off, inkX: x.inkOff, inkY: y.inkOff };
    if ((Math.abs(x.off) > 0.75 && !x.held) || (Math.abs(y.off) > 0.75 && !y.held)) out.wrong.push(from[1]);
  }
  return out;
};

// A face sits with the middle of its head on the middle of its plate, at
// every screen density. Run against the real page with the pictures and
// head boxes the real server gives, this reports how far off each face is
// and which is worst, whether or not anything fails.
export async function faces({ browser, cfg, watch, report }) {
  const measures = { faces: 0, off_centre: 0 };
  const worst = {};
  const worstX = {};
  const wrong = new Set();
  for (const dpr of cfg.faceDensities) {
    const ctx = await openContext(browser, { cfg, watch, cookie: cfg.session.cookie, dpr });
    try {
      const page = await openMap(ctx, cfg);
      const got = await page.evaluate(measureFaces);
      if (got.faces === 0) continue;
      const name = String(dpr).replace('.', '_');
      measures.faces = Math.max(measures.faces, got.faces);
      measures.with_head_box = got.boxed;
      measures.drawn_as_eggs = got.eggs;
      let far = 0;
      let farInk = 0;
      for (const [key, off] of Object.entries(got.offsets)) {
        const head = Math.max(Math.abs(off.x), Math.abs(off.y));
        far = Math.max(far, head);
        farInk = Math.max(farInk, Math.abs(off.inkX), Math.abs(off.inkY));
        if (!(key in worst) || head > worst[key]) worst[key] = Math.round(head * 100) / 100;
        if (!(key in worstX) || Math.abs(off.x) > Math.abs(worstX[key])) worstX[key] = Math.round(off.x * 100) / 100;
      }
      measures[`worst_head_pixels_at_${name}`] = Math.round(far * 100) / 100;
      measures[`worst_ink_pixels_at_${name}`] = Math.round(farInk * 100) / 100;
      for (const key of got.wrong) wrong.add(key);
    } finally {
      await ctx.close();
    }
  }
  if (measures.faces === 0) return skip('the server lists no faces');
  // By the picture's key, which is the game's own name for a mob.
  report.gauge('mcmap_e2e_face_offset_pixels', worst);
  report.gauge('mcmap_e2e_face_offset_x_pixels', worstX);
  measures.off_centre = wrong.size;
  if (wrong.size > 0) throw new Fail('a face is off the middle of its plate with room to be on it', measures);
  return { measures };
}
