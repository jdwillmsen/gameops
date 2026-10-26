// The replay view: scrub a past window of unit positions on the lab map, with
// the events of that window listed beside it. Where units stood is
// operator-only, so signed out this paints a sign-in state and never asks for
// a position. replay.js holds the logic; this file paints.
import {
  SPEEDS, WINDOWS, windowOf, buildTracks, frameAt, trailOf, advance, boundsOf, fit, eventsUpTo, withMs, positionsQuery, eventsQuery,
  fetchAll, summary, clock, MAX_POSITION_PAGES, MAX_EVENT_PAGES, TRAIL_MS,
} from './replay.js';

function el(tag, props = {}, ...kids) {
  const e = document.createElement(tag);
  for (const [k, v] of Object.entries(props)) {
    if (k.includes('-') || k === 'role') e.setAttribute(k, v);
    else e[k] = v;
  }
  e.append(...kids);
  return e;
}

const DOT = 5;

function action(text, run) {
  const b = el('button', { type: 'button', textContent: text });
  b.addEventListener('click', run);
  return b;
}

// host: { statuses, order(), operator(), api(method, path, body, extra) }
export function initReplay(host) {
  const root = el('section', { className: 'rp' });
  root.setAttribute('aria-label', 'Replay');
  let mounted = false;
  let token = 0;
  let raf = 0;
  let lastFrame = 0;
  let sel = { window: '10m', unit: '' };
  let data = null;
  let places = [];
  let ui = null;
  let proj = null;
  let playing = false;
  let speed = 1;
  let time = 0;
  let colors = new Map();

  const nameOf = id => (host.statuses.get(id) || {}).name || id;

  function showState(...kids) {
    stop();
    root.replaceChildren(el('div', { className: 'rp-state' }, ...kids));
    ui = null;
  }

  function paintAuth() {
    showState(
      el('h2', { textContent: 'Sign in to replay' }),
      el('p', {}, 'Where units stood is operator-only. ', el('a', { href: '/signin', textContent: 'Sign in' }), '.'));
  }

  async function load() {
    if (!mounted) return;
    if (!host.operator()) { paintAuth(); return; }
    const my = ++token;
    const until = ui && ui.end.value ? new Date(ui.end.value).getTime() : Date.now();
    const since = until - windowOf(sel.window).ms;
    const current = () => mounted && my === token;
    if (!ui) showState(el('p', { textContent: 'Reading the recorded positions...' }));
    else ui.sum.textContent = 'Reading the recorded positions...';
    const [pos, evs, pl] = await Promise.all([
      fetchAll(host.api, cursor => positionsQuery({ since, until, cursor, limit: 5000 }), 'samples', MAX_POSITION_PAGES, current),
      fetchAll(host.api, cursor => eventsQuery({ since, until, cursor }), 'events', MAX_EVENT_PAGES, current),
      places.length ? Promise.resolve(null) : host.api('GET', '/v1/places', undefined, { cache: 'no-store' }),
    ]);
    if (!current() || pos.aborted || evs.aborted) return;
    if (pl && pl.ok && Array.isArray(pl.data)) places = pl.data;
    if (pos.status === 401 || evs.status === 401) { paintAuth(); return; }
    if (pos.error) {
      showState(el('h2', { textContent: 'Could not read the replay.' }), el('p', { textContent: pos.error }),
        action('Try again', () => load()));
      return;
    }
    const tracks = buildTracks(pos.rows);
    const events = evs.rows.map(withMs);
    // An events failure leaves the map usable; the list says why it is empty.
    data = { tracks, events, since, until, truncated: pos.truncated || evs.truncated, eventsError: evs.error || '' };
    time = since;
    colors = new Map([...tracks.keys()].sort().map((id, i) => [id, i]));
    playing = false;
    build();
    paint();
  }

  function select(label, name, options, value, onChange) {
    const s = el('select', { name, 'aria-label': label });
    for (const [v, text] of options) s.append(el('option', { value: v, textContent: text }));
    s.value = value;
    s.addEventListener('change', onChange);
    return s;
  }

  function setUnit(id) {
    sel = { ...sel, unit: id };
    if (ui) ui.unitSel.value = id;
    paint();
  }

  // Controls and canvas are built once per load and updated in place, so a
  // frame never drops keyboard focus from the scrubber.
  function build() {
    const prevEnd = ui ? ui.end.value : '';
    const windowSel = select('Window', 'window', WINDOWS.map(w => [w.id, w.label]), sel.window, () => { sel = { ...sel, window: windowSel.value }; load(); });
    if (sel.unit && !data.tracks.has(sel.unit)) sel = { ...sel, unit: '' };
    const unitSel = select('Unit', 'unit', [['', 'Whole fleet'], ...[...data.tracks.keys()].sort().map(id => [id, nameOf(id)])], sel.unit, () => setUnit(unitSel.value));
    const end = el('input', { type: 'datetime-local', name: 'end', value: prevEnd, title: 'End of the window; empty means now' });
    end.setAttribute('aria-label', 'Window end');
    end.addEventListener('change', () => load());
    const reload = action('Reload', () => load());
    const canvas = el('canvas', { className: 'rp-map', width: 960, height: 640, tabIndex: 0 });
    canvas.setAttribute('role', 'img');
    canvas.addEventListener('click', onPick);
    const play = action('Play', () => toggle());
    play.className = 'rp-play';
    const speeds = SPEEDS.map(s => {
      const b = action(`${s}x`, () => { speed = s; paintControls(); });
      b.dataset.speed = String(s);
      return b;
    });
    const scrub = el('input', { type: 'range', className: 'rp-scrub', min: 0, max: data.until - data.since, step: 100, value: 0 });
    scrub.setAttribute('aria-label', 'Replay position');
    scrub.addEventListener('input', () => { time = data.since + Number(scrub.value); paint(); });
    ui = {
      windowSel, unitSel, end, play, speeds, scrub,
      title: el('h2', { textContent: 'Replay' }), sum: el('p', { className: 'rp-sum' }),
      clock: el('output', { className: 'rp-clock' }),
      canvas, warn: el('p', { className: 'rp-warn', role: 'status' }),
      events: el('ol', { className: 'rp-events', 'aria-label': 'Events at this moment' }),
      evHead: el('h3', { textContent: 'Events' }),
    };
    ui.bar = el('div', { className: 'rp-bar' }, windowSel, end, reload, unitSel);
    ui.transport = el('div', { className: 'rp-transport', role: 'group', 'aria-label': 'Playback' }, play, ...speeds, ui.clock);
    const side = el('aside', { className: 'rp-side' }, ui.evHead, ui.events);
    root.replaceChildren(ui.title, ui.sum, ui.bar, ui.warn,
      el('div', { className: 'rp-body' }, el('div', { className: 'rp-stage' }, canvas, ui.transport, scrub), side));
  }

  function paintControls() {
    ui.play.textContent = playing ? 'Pause' : 'Play';
    ui.play.setAttribute('aria-pressed', String(playing));
    for (const b of ui.speeds) b.setAttribute('aria-pressed', String(Number(b.dataset.speed) === speed));
  }

  function paint() {
    if (!ui || !data) return;
    ui.sum.textContent = summary(data.tracks, data.events, data.since, data.until);
    const notes = [];
    if (data.truncated) notes.push('This range is larger than the view reads; the end of it is missing. Pick a shorter window.');
    if (data.eventsError) notes.push(`Events could not be read: ${data.eventsError}`);
    ui.warn.textContent = notes.join(' ');
    ui.clock.textContent = `${clock(time)} (${((time - data.since) / 1000).toFixed(0)}s of ${((data.until - data.since) / 1000).toFixed(0)}s)`;
    ui.scrub.value = String(time - data.since);
    ui.scrub.setAttribute('aria-valuetext', clock(time));
    paintControls();
    const shown = eventsUpTo(data.events, time, 14, sel.unit);
    ui.events.replaceChildren(...(shown.length
      ? shown.map(e => el('li', { className: `sev-${e.severity}` },
        el('time', { textContent: clock(e.ms) }), ' ', el('b', { textContent: nameOf(e.unit) }), ` ${e.kind}`,
        e.pos ? ` at ${Math.round(e.pos.x)}, ${Math.round(e.pos.z)}` : ''))
      : [el('li', { className: 'rp-none', textContent: data.eventsError ? 'Unavailable.' : 'No events yet at this moment.' })]));
    ui.canvas.setAttribute('aria-label', `Replay map at ${clock(time)}. ${summary(data.tracks, data.events, data.since, data.until)}`);
    draw();
  }

  function draw() {
    const { canvas } = ui;
    const ctx = canvas.getContext('2d');
    const w = canvas.width, h = canvas.height;
    const cs = getComputedStyle(root);
    const b = boundsOf(data.tracks, places.map(p => p.box).filter(Boolean));
    proj = fit(b, w, h);
    ctx.fillStyle = cs.getPropertyValue('--rp-bg').trim() || '#0b100c';
    ctx.fillRect(0, 0, w, h);
    ctx.strokeStyle = 'rgba(200,212,200,0.18)';
    ctx.lineWidth = 1;
    ctx.font = '11px ui-monospace, monospace';
    ctx.fillStyle = 'rgba(200,212,200,0.7)';
    for (let x = Math.ceil(b.minX / 64) * 64; x <= b.maxX; x += 64) {
      ctx.beginPath(); ctx.moveTo(proj.x(x), 0); ctx.lineTo(proj.x(x), h); ctx.stroke();
      ctx.fillText(`x ${x}`, proj.x(x) + 3, 12);
    }
    for (let z = Math.ceil(b.minZ / 64) * 64; z <= b.maxZ; z += 64) {
      ctx.beginPath(); ctx.moveTo(0, proj.y(z)); ctx.lineTo(w, proj.y(z)); ctx.stroke();
      ctx.fillText(`z ${z}`, 3, proj.y(z) - 3);
    }
    ctx.strokeStyle = 'rgba(240,240,240,0.4)';
    for (const p of places) {
      if (!p.box) continue;
      const x = proj.x(p.box.min.x), y = proj.y(p.box.min.z);
      ctx.strokeRect(x, y, (p.box.max.x - p.box.min.x + 1) * proj.scale, (p.box.max.z - p.box.min.z + 1) * proj.scale);
      ctx.fillText(p.name || p.id, x + 3, y + 12);
    }
    const hues = Math.max(1, colors.size);
    const color = id => `hsl(${Math.round(((colors.get(id) || 0) / hues) * 360)} 70% 62%)`;
    for (const f of frameAt(data.tracks, time)) {
      const picked = sel.unit === f.unit;
      const dim = sel.unit && !picked;
      ctx.globalAlpha = dim ? 0.35 : 1;
      if (picked || !sel.unit) {
        const trail = trailOf(data.tracks.get(f.unit), time, picked ? TRAIL_MS * 3 : TRAIL_MS);
        ctx.strokeStyle = color(f.unit);
        ctx.lineWidth = picked ? 2 : 1;
        ctx.beginPath();
        trail.forEach((p, i) => (i ? ctx.lineTo(proj.x(p.x), proj.y(p.z)) : ctx.moveTo(proj.x(p.x), proj.y(p.z))));
        ctx.stroke();
      }
      ctx.fillStyle = color(f.unit);
      ctx.beginPath();
      ctx.arc(proj.x(f.x), proj.y(f.z), picked ? DOT + 2 : DOT, 0, Math.PI * 2);
      ctx.fill();
      ctx.fillStyle = 'rgba(230,238,230,0.95)';
      ctx.fillText(nameOf(f.unit), proj.x(f.x) + DOT + 3, proj.y(f.z) + 4);
    }
    ctx.globalAlpha = 1;
    ctx.lineWidth = 1;
  }

  function onPick(ev) {
    if (!proj || !data) return;
    const r = ui.canvas.getBoundingClientRect();
    const px = ((ev.clientX - r.left) / r.width) * ui.canvas.width;
    const py = ((ev.clientY - r.top) / r.height) * ui.canvas.height;
    let best = null, bestD = (DOT + 6) ** 2;
    for (const f of frameAt(data.tracks, time)) {
      const d = (proj.x(f.x) - px) ** 2 + (proj.y(f.z) - py) ** 2;
      if (d < bestD) { best = f.unit; bestD = d; }
    }
    setUnit(best === sel.unit ? '' : (best || ''));
  }

  function tick(now) {
    if (!playing) return;
    const step = advance(time, now - lastFrame, speed, data.until);
    lastFrame = now;
    time = step.time;
    if (step.ended) playing = false;
    paint();
    if (playing) raf = requestAnimationFrame(tick);
  }

  function toggle() {
    if (!data) return;
    if (playing) { stop(); paint(); return; }
    // Play from the end restarts, as a media player does.
    if (time >= data.until) time = data.since;
    playing = true;
    lastFrame = performance.now();
    raf = requestAnimationFrame(tick);
    paintControls();
  }

  function stop() {
    playing = false;
    cancelAnimationFrame(raf);
  }

  const onResize = () => { if (data && ui) draw(); };

  return {
    mount(into) {
      mounted = true;
      data = null;
      into.append(root);
      showState(el('p', { textContent: 'Reading the recorded positions...' }));
      load();
      addEventListener('resize', onResize);
    },
    unmount() {
      if (!mounted) return;
      mounted = false;
      stop();
      removeEventListener('resize', onResize);
      root.remove();
      // Positions must not outlive the session or the view.
      data = null;
      places = [];
      ui = null;
      token++;
    },
    onOperator() {
      if (!mounted) return;
      token++;
      if (!host.operator()) { data = null; places = []; paintAuth(); } else { ui = null; load(); }
    },
    // Space plays and pauses; the arrows step five seconds. Returns true when
    // the key was used.
    key(ev) {
      if (!mounted || !data || !ui) return false;
      // A focused control already answers these keys itself.
      if (ev.target && /^(BUTTON|INPUT|SELECT|TEXTAREA|A)$/.test(ev.target.tagName)) return false;
      if (ev.key === ' ') { toggle(); return true; }
      if (ev.key === 'ArrowLeft' || ev.key === 'ArrowRight') {
        time = Math.min(data.until, Math.max(data.since, time + (ev.key === 'ArrowRight' ? 5000 : -5000)));
        paint();
        return true;
      }
      return false;
    },
  };
}
