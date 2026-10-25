// The interactive node graph: SVG, pan/zoom, keyboard and in-place state
// updates. Layout lives in layout.js and stays pure; this file owns the DOM.
// setData() rebuilds drawn nodes and edges only when the structure (ids and
// edges) changed, so a state update patches text, classes and chips and never
// moves a node or the viewport — no re-layout flicker.

import { layout as layoutDag } from './layout.js';

export const NODE_W = 168, NODE_H = 48, COL_GAP = 56, ROW_GAP = 14, MARGIN = 24;
const ZOOM_MIN = 0.15, ZOOM_MAX = 3;
const SUB_MAX = 26;

// SVG text has no ellipsis; keep a long task line from being cut mid-glyph.
const clip = (s, n = SUB_MAX) => (s.length > n ? s.slice(0, n - 1) + '…' : s);

function sEl(name, attrs = {}, ...kids) {
  const e = document.createElementNS('http://www.w3.org/2000/svg', name);
  for (const [k, v] of Object.entries(attrs)) e.setAttribute(k, v);
  e.append(...kids);
  return e;
}

function el(tag, props = {}, ...kids) {
  const e = document.createElement(tag);
  for (const [k, v] of Object.entries(props)) {
    if (k.includes('-')) e.setAttribute(k, v);
    else e[k] = v;
  }
  e.append(...kids);
  return e;
}

function svgIcon(paths, fill) {
  const svg = sEl('svg', { width: 10, height: 10, class: 'dg-ico', viewBox: '0 0 12 12', 'aria-hidden': 'true' });
  for (const d of [].concat(paths)) {
    // fill marks the closed shapes: an outline is its own identity, not a
    // lighter copy of the filled word.
    svg.append(sEl('path', {
      d, fill: fill ? 'currentColor' : 'none', stroke: 'currentColor', 'stroke-width': '1.6',
      'stroke-linecap': 'round', 'stroke-linejoin': 'round',
    }));
  }
  return svg;
}

// One shape per state plus its word, so colour is a third channel, never the
// only one. The caller supplies the state -> {word, tone, icon paths, fill}
// list; the legend and every node chip draw through the same helper.
function chipHtml(c) {
  return el('span', { className: `dg-chip dg-${c.tone}` }, svgIcon(c.icon, c.fill), el('span', { className: 'dg-word', textContent: c.word }));
}

export function createDagGraph({ label, onSelect = () => {}, onClose = () => {}, onLayout } = {}) {
  const root = el('section', { className: 'dg cc-root' });
  root.setAttribute('data-theme', 'dark');
  root.setAttribute('aria-label', label || 'Node graph');

  // ---- toolbar, legend and cycle note ----
  const legend = el('div', { className: 'dg-legend', role: 'list', 'aria-label': 'State legend' });
  const note = el('p', { className: 'dg-note', role: 'status', hidden: true });
  const zoomIn = el('button', { type: 'button', textContent: '+', title: 'Zoom in', 'aria-label': 'Zoom in' });
  const zoomOut = el('button', { type: 'button', textContent: '−', title: 'Zoom out', 'aria-label': 'Zoom out' });
  const fitBtn = el('button', { type: 'button', textContent: 'Fit', title: 'Fit to screen', 'aria-label': 'Fit graph to screen' });
  const tools = el('div', { className: 'dg-tools' }, el('div', { className: 'dg-zooms' }, zoomOut, zoomIn, fitBtn), legend,
    el('span', { className: 'dg-key', textContent: 'thicker outline and edge: critical path' }), note);

  // ---- the svg viewport ----
  const svg = sEl('svg', { class: 'dg-canvas', 'aria-label': 'Graph canvas' }, sEl('defs', {},
    sEl('marker', {
      id: 'dg-arrow', class: 'dg-arrow', viewBox: '0 0 10 10', refX: 9, refY: 5,
      markerWidth: 7, markerHeight: 7, orient: 'auto-start-reverse',
    }, sEl('path', { d: 'M0 0 L10 5 L0 10 Z', fill: 'currentColor', stroke: 'none' })),
    sEl('clipPath', { id: 'dg-clip' }, sEl('rect', { x: 8, y: 0, width: NODE_W - 16, height: NODE_H })),
  ));
  svg.style.touchAction = 'none';
  const zoomLayer = sEl('g', { class: 'dg-zoom' });
  const edgeLayer = sEl('g', { class: 'dg-edges' });
  const nodeLayer = sEl('g', { class: 'dg-nodes' });
  zoomLayer.append(edgeLayer, nodeLayer);
  svg.append(zoomLayer);
  const side = el('aside', { className: 'dg-side', 'aria-label': 'Details', hidden: true });
  root.append(tools, el('div', { className: 'dg-body' }, svg, side));

  // ---- pan and zoom ----
  const view = { x: 0, y: 0, k: 1, moved: false };
  const apply = () => zoomLayer.setAttribute('transform', `translate(${view.x} ${view.y}) scale(${view.k})`);
  const setZoom = (k, cx, cy) => {
    const to = Math.min(ZOOM_MAX, Math.max(ZOOM_MIN, k));
    if (cx !== undefined) {
      // Keep what the pointer points at under it while the scale changes.
      view.x = cx - (cx - view.x) * (to / view.k);
      view.y = cy - (cy - view.y) * (to / view.k);
    }
    view.k = to;
    view.moved = true;
    apply();
  };
  zoomIn.addEventListener('click', () => setZoom(view.k * 1.25));
  zoomOut.addEventListener('click', () => setZoom(view.k / 1.25));

  let size = { w: 0, h: 0 };
  let graphResult = null;
  let drag = null;
  // A drag must not be a click: this flips in pointermove before the pointerup
  // would synthesise the click on a node.
  let dragMoved = false;

  svg.addEventListener('pointerdown', ev => {
    drag = { x: ev.clientX, y: ev.clientY, ox: view.x, oy: view.y, moved: false };
    dragMoved = false;
  });
  svg.addEventListener('pointermove', ev => {
    if (!drag) return;
    const dx = ev.clientX - drag.x, dy = ev.clientY - drag.y;
    if (!drag.moved) {
      if (Math.abs(dx) + Math.abs(dy) <= 5) return;
      drag.moved = true;
      dragMoved = true;
      // Capture only once this is a drag: capturing on pointerdown would
      // retarget the click and a plain node click would never reach the node.
      svg.setPointerCapture(ev.pointerId);
    }
    view.x = drag.ox + dx;
    view.y = drag.oy + dy;
    view.moved = true;
    apply();
  });
  const endDrag = () => { drag = null; };
  svg.addEventListener('pointerup', endDrag);
  svg.addEventListener('pointercancel', endDrag);
  svg.addEventListener('wheel', ev => {
    ev.preventDefault();
    const r = svg.getBoundingClientRect();
    setZoom(view.k * Math.exp(-ev.deltaY * 0.0015), ev.clientX - r.left, ev.clientY - r.top);
  }, { passive: false });

  function fit() {
    const w = svg.clientWidth, h = svg.clientHeight;
    if (!w || !h || size.w <= 0 || size.h <= 0) return;
    const k = Math.min(ZOOM_MAX, Math.max(ZOOM_MIN, Math.min(w / (size.w + MARGIN * 2), h / (size.h + MARGIN * 2), 1.6)));
    view.k = k;
    // Nodes start at MARGIN inside the zoom layer, so the margin is part
    // of what is fitted and centred; dropping it clips the right and bottom.
    view.x = (w - size.w * k) / 2 - MARGIN * k;
    view.y = (h - size.h * k) / 2 - MARGIN * k;
    view.moved = false;
    apply();
  }
  fitBtn.addEventListener('click', fit);
  new ResizeObserver(() => { if (!view.moved) fit(); }).observe(svg);

  // ---- drawing ----
  let sig = '';
  let critEdges = new Set();
  const drawn = new Map(); // id -> {g, label, sub, chipHost}
  const edgeEls = [];

  function clearAll() {
    for (const d of drawn.values()) d.g.remove();
    drawn.clear();
    for (const e of edgeEls) e.remove();
    edgeEls.length = 0;
  }

  function renderStructure(nodes, edges) {
    graphResult = layoutDag(nodes.map(n => ({ id: n.id })), edges);
    clearAll();
    critEdges = new Set();
    const ids = graphResult.criticalIds;
    for (let i = 1; i < ids.length; i++) critEdges.add(`${ids[i - 1]}\u0000${ids[i]}`);
    // The graph's own adjacency is deduplicated and rank-sorted, so duplicate
    // or dangling input edges never draw twice or in a varying order.
    for (const [from, list] of graphResult.graph.succ) {
      for (const to of list) {
        const a = graphResult.pos.get(from), b = graphResult.pos.get(to);
        if (!a || !b) continue;
        const x1 = MARGIN + a.x * (NODE_W + COL_GAP) + NODE_W, y1 = MARGIN + a.y * (NODE_H + ROW_GAP) + NODE_H / 2;
        const x2 = MARGIN + b.x * (NODE_W + COL_GAP), y2 = MARGIN + b.y * (NODE_H + ROW_GAP) + NODE_H / 2;
        const mid = (x1 + x2) / 2;
        const path = sEl('path', {
          d: `M${x1} ${y1} C ${mid} ${y1}, ${mid} ${y2}, ${x2} ${y2}`,
          class: critEdges.has(`${from}\u0000${to}`) ? 'dg-edge dg-crit' : 'dg-edge',
          'marker-end': 'url(#dg-arrow)',
        });
        edgeLayer.append(path);
        edgeEls.push(path);
      }
    }
    for (const n of nodes) {
      const pos = graphResult.pos.get(n.id) || { x: 0, y: 0 };
      const g = sEl('g', {
        class: 'dg-node',
        tabindex: '0',
        role: 'button',
        'data-id': n.id,
        transform: `translate(${MARGIN + pos.x * (NODE_W + COL_GAP)} ${MARGIN + pos.y * (NODE_H + ROW_GAP)})`,
      });
      const chipHost = sEl('foreignObject', { x: 8, y: NODE_H - 17, width: NODE_W - 16, height: 15 });
      g.append(
        sEl('rect', { class: 'dg-box', x: 0, y: 0, width: NODE_W, height: NODE_H, rx: 6 }),
        sEl('text', { class: 'dg-label', x: 10, y: 17, 'clip-path': 'url(#dg-clip)' }),
        sEl('text', { class: 'dg-sub', x: 10, y: 31, 'clip-path': 'url(#dg-clip)' }),
        chipHost,
      );
      g.addEventListener('click', ev => {
        ev.stopPropagation();
        if (!dragMoved) onSelect(n.id);
      });
      g.addEventListener('keydown', ev => onNodeKey(ev, n.id));
      nodeLayer.append(g);
      drawn.set(n.id, { g, label: g.querySelector('.dg-label'), sub: g.querySelector('.dg-sub'), chipHost });
    }
    size = {
      w: graphResult.width ? graphResult.width * (NODE_W + COL_GAP) - COL_GAP : 0,
      h: graphResult.height ? graphResult.height * (NODE_H + ROW_GAP) - ROW_GAP : 0,
    };
    note.hidden = !graphResult.cycle.length;
    if (graphResult.cycle.length) {
      note.textContent = `steps depend on each other in a cycle: ${graphResult.cycle.join(', ')} — drawn at the end; the scheduler would never run them`;
    }
    // A pan the operator made should survive small structural changes, so
    // refit only while the view has not been personalised.
    if (!view.moved) fit();
    if (onLayout) onLayout(graphResult);
  }

  function patchChip(host, c) {
    const chip = host.firstChild;
    if (!c) {
      if (chip) chip.remove();
      return;
    }
    // The icon is part of the state's identity, so a state change replaces the
    // whole chip; a mere text patch would keep the previous state's shape and
    // leave the icon lying about the word.
    const sig = `${c.word}\u0000${c.tone}\u0000${JSON.stringify(c.icon)}\u0000${c.fill}`;
    if (chip && chip.dataset.sig === sig) return;
    const next = chipHtml(c);
    next.dataset.sig = sig;
    if (chip) chip.replaceWith(next);
    else host.append(next);
  }

  function renderState({ nodes }) {
    for (const n of nodes) {
      const d = drawn.get(n.id);
      if (!d) continue;
      d.g.classList.toggle('dg-on', !!n.selected);
      d.g.classList.toggle('dg-path', graphResult.critical.has(n.id));
      d.g.setAttribute('data-tone', n.tone || 'plain');
      if (d.label.textContent !== n.label) d.label.textContent = n.label;
      const subText = clip(n.sub || '');
      if (d.sub.textContent !== subText) {
        d.sub.textContent = subText;
        d.sub.setAttribute('fill-opacity', subText ? '1' : '0');
      }
      patchChip(d.chipHost, n.chip);
      const name = `${n.label}${n.sub ? `, ${n.sub}` : ''}${n.chip ? `, ${n.chip.word}` : ''}`;
      const row = `${name}${n.note ? `, ${n.note}` : ''}`;
      if (d.g.getAttribute('aria-label') !== row) d.g.setAttribute('aria-label', row);
    }
  }

  let dataset = '';
  function setData(data) {
    // A different job is a different page, not a live patch: drop the old pan
    // and zoom so the new graph opens fitted. State patches keep the same id.
    if (data.id !== dataset) {
      dataset = data.id || '';
      view.x = view.y = 0;
      view.k = 1;
      view.moved = false;
      sig = '';
    }
    const nextSig = `${data.nodes.map(n => n.id).join('|')}#${data.edges.map(e => `${e.from}>${e.to}`).join(',')}`;
    if (nextSig !== sig) {
      sig = nextSig;
      renderStructure(data.nodes, data.edges);
    }
    renderState(data);
  }

  // ---- keyboard on a node ----
  function focusNode(id) {
    const d = drawn.get(id);
    if (!d) return;
    d.g.focus({ preventScroll: false });
  }

  // What this step unlocks, what it waited for, and its row neighbours, in
  // screen order — layout.js hands over the rank-sorted adjacency lists.
  const downstreamOf = id => graphResult ? graphResult.graph.succ.get(id) : [];
  const upstreamOf = id => graphResult ? graphResult.graph.pred.get(id) : [];

  function siblingOf(id, dir) {
    if (!graphResult) return null;
    const l = graphResult.layerOf.get(id);
    if (l === undefined) return null;
    // Cycle nodes sit past the last drawn column; they have no siblings.
    const col = graphResult.layers[l];
    if (!col) return null;
    const at = col.indexOf(id);
    if (at < 0) return null;
    const next = at + dir;
    return next >= 0 && next < col.length ? col[next] : null;
  }

  function onNodeKey(ev, id) {
    // Right follows the edge to what the step unlocks, left goes back to what
    // it waited for, up and down step through the same layer. Enter opens the
    // details panel like a click does. Handled keys stop here so the console's
    // global keys (Esc, arrows in another view) cannot act on the same press.
    if (ev.key === 'Enter' || ev.key === ' ') {
      ev.preventDefault();
      ev.stopPropagation();
      onSelect(id);
      return;
    }
    if (ev.key === 'Escape') {
      ev.preventDefault();
      ev.stopPropagation();
      onClose();
      return;
    }
    let to;
    if (ev.key === 'ArrowRight') to = downstreamOf(id)[0];
    else if (ev.key === 'ArrowLeft') to = upstreamOf(id)[0];
    else if (ev.key === 'ArrowDown') to = siblingOf(id, 1);
    else if (ev.key === 'ArrowUp') to = siblingOf(id, -1);
    if (to) {
      ev.preventDefault();
      ev.stopPropagation();
      focusNode(to);
    } else if (ev.key.startsWith('Arrow')) {
      // A leaf ends the walk along that direction; holding focus beats
      // jumping somewhere unconnected.
      ev.preventDefault();
      ev.stopPropagation();
    }
  }

  return {
    root, side, note,
    setData,
    setLegend(states) {
      legend.replaceChildren(...states.map(c => chipHtml(c)));
    },
    setContent: c => {
      side.replaceChildren(...(c ? [c] : []));
      side.hidden = !c;
      if (c) {
        const first = c.querySelector('[data-autofocus]');
        if (first) first.focus();
      }
    },
    open: focusNode,
    clear: () => { if (root.contains(document.activeElement)) document.activeElement.blur(); },
    downstreamOf, upstreamOf, siblingOf,
  };
}
