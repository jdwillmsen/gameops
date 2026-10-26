// The heatmap view: a top-down lab map with a density layer over it. Where
// units stand is operator-only, so signed out this paints a sign-in state
// and never asks for a grid. heat.js holds the logic; this file paints.
import {
  LEVELS, LAYERS, WINDOWS, layerOf, levelOf, legendSteps, boundsOf, fit, cellIndex, cellAt, hottest, summary, heatQuery,
} from './heat.js';

const POLL_MS = 10_000;

function el(tag, props = {}, ...kids) {
  const e = document.createElement(tag);
  for (const [k, v] of Object.entries(props)) {
    if (k.includes('-') || k === 'role') e.setAttribute(k, v);
    else e[k] = v;
  }
  e.append(...kids);
  return e;
}

// host: { statuses, order(), operator(), api(method, path, body, extra) }
export function initHeat(host) {
  const root = el('section', { className: 'hm' });
  root.setAttribute('aria-label', 'Heatmap');
  let mounted = false;
  let timer = 0;
  let token = 0;
  let sel = { layer: 'position', window: '1h', unit: '' };
  let grid = null;
  let places = [];
  let canvas = null;
  let ui = null;
  let readout = null;
  let index = new Map();
  let proj = null;

  const ramp = () => {
    const cs = getComputedStyle(root);
    return Array.from({ length: LEVELS }, (_, i) => cs.getPropertyValue(`--hm-${i + 1}`).trim() || '#888');
  };

  function select(label, name, options, value) {
    const s = el('select', { name, 'aria-label': label });
    for (const [v, text] of options) s.append(el('option', { value: v, textContent: text }));
    s.value = value;
    s.addEventListener('change', () => { sel = { ...sel, [name]: s.value }; load(); });
    return s;
  }

  function showState(...kids) {
    root.replaceChildren(el('div', { className: 'hm-state' }, ...kids));
    canvas = null;
    ui = null;
  }

  function paintAuth() {
    showState(
      el('h2', { textContent: 'Sign in to see the heatmap' }),
      el('p', {}, 'Where units stand is operator-only. ', el('a', { href: '/signin', textContent: 'Sign in' }), '.'));
  }

  async function load() {
    if (!mounted) return;
    if (!host.operator()) { paintAuth(); return; }
    const my = ++token;
    const [r, p] = await Promise.all([
      host.api('GET', heatQuery(sel), undefined, { cache: 'no-store' }),
      places.length ? Promise.resolve(null) : host.api('GET', '/v1/places', undefined, { cache: 'no-store' }),
    ]);
    if (!mounted || my !== token) return;
    if (p && p.ok && Array.isArray(p.data)) places = p.data;
    if (r.status === 401) { paintAuth(); return; }
    if (!r.ok || !r.data) {
      if (grid) { root.classList.add('stale'); return; }
      showState(el('h2', { textContent: 'Could not read the heatmap.' }), el('p', { textContent: (r.data && r.data.error) || `Failed (${r.status}).` }));
      return;
    }
    root.classList.remove('stale');
    grid = r.data;
    index = cellIndex(grid);
    paint();
  }

  // The controls and canvas are built once and updated in place: replacing
  // them on every poll would drop keyboard focus and close an open dropdown.
  function build() {
    const layerSel = select('Layer', 'layer', LAYERS.map(l => [l.id, l.label]), sel.layer);
    const windowSel = select('Window', 'window', WINDOWS.map(w => [w, `last ${w}`]), sel.window);
    const unitSel = select('Unit', 'unit', [['', 'Whole fleet']], sel.unit);
    canvas = el('canvas', { className: 'hm-map', width: 960, height: 640, tabIndex: 0 });
    canvas.setAttribute('role', 'img');
    canvas.addEventListener('pointermove', onMove);
    canvas.addEventListener('pointerleave', () => { readout.textContent = ''; });
    readout = el('p', { className: 'hm-readout', 'aria-live': 'off' });
    ui = {
      unitSel, unitKey: '',
      title: el('h2'), sum: el('p', { className: 'hm-sum' }),
      bar: el('div', { className: 'hm-bar' }, layerSel, windowSel, unitSel),
      legend: el('div', { className: 'hm-legend', role: 'group', 'aria-label': 'Legend, samples per cell' }),
      topHead: el('h3', { textContent: 'Busiest cells' }), list: el('ol', { className: 'hm-top' }),
    };
    root.replaceChildren(ui.title, ui.sum, ui.bar, canvas, readout, ui.legend, ui.topHead, ui.list);
  }

  function paint() {
    if (!ui || ui.title.parentNode !== root) build();
    const units = host.order().map(id => [id, (host.statuses.get(id) || {}).name || id]);
    const key = JSON.stringify(units);
    if (key !== ui.unitKey) {
      ui.unitKey = key;
      ui.unitSel.replaceChildren(...[['', 'Whole fleet'], ...units].map(([v, text]) => el('option', { value: v, textContent: text })));
      ui.unitSel.value = sel.unit;
    }
    ui.title.textContent = `${layerOf(sel.layer).label} heatmap`;
    ui.sum.textContent = `${layerOf(sel.layer).hint}. ${summary(grid)}`;
    canvas.setAttribute('aria-label', `${layerOf(sel.layer).label} density over the lab map. ${summary(grid)}`);

    const legend = [el('span', { textContent: 'fewer' })];
    for (const s of legendSteps(grid.max)) {
      const sw = el('span', { className: 'hm-sw' }, el('i', { textContent: '' }), el('b', { textContent: String(s.from) }));
      sw.firstChild.style.background = `var(--hm-${s.level})`;
      legend.push(sw);
    }
    legend.push(el('span', { textContent: 'more' }));
    ui.legend.replaceChildren(...legend);

    ui.list.replaceChildren(...hottest(grid).map(([cx, cz, n]) =>
      el('li', { textContent: `x ${cx * grid.cell}..${cx * grid.cell + grid.cell - 1}, z ${cz * grid.cell}..${cz * grid.cell + grid.cell - 1}: ${n}` })));
    ui.topHead.hidden = ui.list.hidden = !grid.cells.length;
    draw();
  }

  function draw() {
    if (!canvas || !grid) return;
    const ctx = canvas.getContext('2d');
    const w = canvas.width, h = canvas.height;
    const boxes = places.map(p => p.box).filter(Boolean);
    const b = boundsOf(grid, boxes);
    proj = fit(b, w, h);
    ctx.fillStyle = getComputedStyle(root).getPropertyValue('--hm-bg').trim() || '#0b100c';
    ctx.fillRect(0, 0, w, h);
    const colors = ramp();
    const size = grid.cell;
    for (const [cx, cz, n] of grid.cells) {
      const lv = levelOf(n, grid.max);
      ctx.fillStyle = colors[lv - 1];
      ctx.fillRect(proj.x(cx * size), proj.y(cz * size), size * proj.scale + 0.5, size * proj.scale + 0.5);
    }
    // Chunk lines and place outlines go over the density so they stay legible
    // on the darkest and the brightest cells alike.
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
    ctx.strokeStyle = 'rgba(240,240,240,0.55)';
    for (const p of places) {
      if (!p.box) continue;
      const x = proj.x(p.box.min.x), y = proj.y(p.box.min.z);
      ctx.strokeRect(x, y, (p.box.max.x - p.box.min.x + 1) * proj.scale, (p.box.max.z - p.box.min.z + 1) * proj.scale);
      ctx.fillText(p.name || p.id, x + 3, y + 12);
    }
  }

  function onMove(ev) {
    if (!proj || !grid) return;
    const r = canvas.getBoundingClientRect();
    const px = ((ev.clientX - r.left) / r.width) * canvas.width;
    const py = ((ev.clientY - r.top) / r.height) * canvas.height;
    const c = cellAt(grid, index, proj.wx(px), proj.wz(py));
    readout.textContent = `x ${c.x0}..${c.x1}, z ${c.z0}..${c.z1}: ${c.n} ${c.n === 1 ? 'sample' : 'samples'}`;
  }

  const onResize = () => draw();

  return {
    mount(into) {
      mounted = true;
      grid = null;
      into.append(root);
      showState(el('p', { textContent: 'Reading the heatmap...' }));
      load();
      timer = setInterval(() => { if (document.visibilityState === 'visible') load(); }, POLL_MS);
      addEventListener('resize', onResize);
    },
    unmount() {
      if (!mounted) return;
      mounted = false;
      clearInterval(timer);
      removeEventListener('resize', onResize);
      root.remove();
      // The grid holds positions: it must not outlive the session or the view.
      grid = null;
      index = new Map();
      places = [];
      token++;
    },
    onOperator() {
      if (!mounted) return;
      token++;
      if (!host.operator()) { grid = null; places = []; paintAuth(); } else load();
    },
  };
}
