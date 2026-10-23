import { h } from './dom.js';

let tip = null;

function ensure() {
  if (tip) return tip;
  tip = {
    root: h('div', { class: 'cc-root cc-tip', hidden: true }),
    title: h('div', { class: 'cc-tip-title' }),
    rows: h('div', { class: 'cc-tip-rows' }),
  };
  tip.root.append(tip.title, tip.rows);
  document.body.append(tip.root);
  return tip;
}

// One tooltip for every chart on the page. Series and category names are
// untrusted (CSV headers, API output), so they only ever reach textContent.
// rows: [{ cls, shape: 'line' | 'bar', name, value, hot }]
export function showTip(anchor, clientX, clientY, { title, rows, foot }) {
  const t = ensure();
  const theme = anchor.closest('[data-theme]');
  if (theme) t.root.setAttribute('data-theme', theme.getAttribute('data-theme'));
  else t.root.removeAttribute('data-theme');
  const texture = anchor.closest('[data-texture]');
  if (texture) t.root.setAttribute('data-texture', texture.getAttribute('data-texture'));
  else t.root.removeAttribute('data-texture');

  t.title.textContent = title;
  t.title.hidden = !title;
  const els = rows.map(r => {
    const row = h('div', { class: r.hot ? 'cc-tip-row cc-hot' : 'cc-tip-row' });
    if (r.cls) row.append(h('span', { class: `cc-key cc-key-${r.shape || 'line'} ${r.cls}` }));
    row.append(h('span', { class: 'cc-tip-value', text: r.value }), h('span', { class: 'cc-tip-name', text: r.name }));
    return row;
  });
  if (foot) els.push(h('div', { class: 'cc-tip-foot', text: foot }));
  t.rows.replaceChildren(...els);
  t.root.hidden = false;

  const pad = 12;
  const w = t.root.offsetWidth;
  const hgt = t.root.offsetHeight;
  let x = clientX + pad;
  let y = clientY + pad;
  if (x + w > innerWidth - 4) x = clientX - pad - w;
  if (y + hgt > innerHeight - 4) y = clientY - pad - hgt;
  t.root.style.transform = `translate(${Math.max(4, x)}px, ${Math.max(4, y)}px)`;
}

export function hideTip() {
  if (tip) tip.root.hidden = true;
}
