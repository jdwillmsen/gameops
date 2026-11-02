// The bulk recall / assign dialog. It shows what will happen to which units
// and only then offers the send button, which names the count.
import { buildRequest, confirmText, outcome, MAX_UNITS } from './bulk.js';
import { GATHER_ITEMS } from './state.js';
import { h } from './kit/dom.js';

// host: { api(method, path, body), name(id), expired(), done(outcome) }
// Returns the overlay element so the caller's Esc handling can remove it.
export function openBulk(host, action, ids, close) {
  const names = ids.map(host.name);
  const nameOf = Object.fromEntries(ids.map((id, i) => [id, names[i]]));
  const field = (label, input) => {
    const l = h('label', { className: 'bk-field' }, h('span', { textContent: label }), input);
    return l;
  };
  const num = (label, v) => field(label, h('input', { type: 'text', inputMode: 'numeric', value: v, size: 5, autocomplete: 'off' }));
  const point = (title, init) => {
    const x = num(`${title} x`, init[0]), y = num(`${title} y`, init[1]), z = num(`${title} z`, init[2]);
    const read = () => ({ x: x.querySelector('input').value, y: y.querySelector('input').value, z: z.querySelector('input').value });
    return { nodes: [x, y, z], read };
  };

  const form = h('form', { className: 'bk-form' });
  const to = point('Destination', ['', '', '']);
  const chest = point('Chest', ['', '', '']);
  const from = point('From', ['', '', '']);
  const template = h('select', { 'aria-label': 'Task' }, h('option', { value: 'gather', textContent: 'Gather and deposit' }), h('option', { value: 'haul', textContent: 'Haul between chests' }));
  const item = h('select', { 'aria-label': 'Item' }, ...GATHER_ITEMS.map(i => h('option', { value: i, textContent: i })));
  const qty = h('input', { type: 'text', inputMode: 'numeric', value: '16', size: 4, autocomplete: 'off', 'aria-label': 'Quantity' });
  const fromRow = h('div', { className: 'bk-row' }, ...from.nodes);

  if (action === 'recall') form.append(h('div', { className: 'bk-row' }, ...to.nodes));
  else form.append(h('div', { className: 'bk-row' }, template, item, qty), h('div', { className: 'bk-row' }, ...chest.nodes), fromRow);

  const summary = h('p', { className: 'bk-summary', role: 'status' });
  const msg = h('p', { className: 'session-error', role: 'alert' });
  const send = h('button', { type: 'submit', className: 'danger' });
  const cancel = h('button', { type: 'button', textContent: 'Cancel' });
  cancel.addEventListener('click', close);
  form.append(summary, msg, h('div', { className: 'bk-row' }, send, cancel));

  const current = () => buildRequest(action, ids, action === 'recall'
    ? { to: to.read() }
    : { template: template.value, item: item.value, qty: qty.value, chest: chest.read(), from: from.read() });

  function refresh() {
    fromRow.hidden = action !== 'assign' || template.value !== 'haul';
    const r = current();
    send.textContent = action === 'recall' ? `Recall ${ids.length} unit${ids.length === 1 ? '' : 's'}` : `Assign to ${ids.length} unit${ids.length === 1 ? '' : 's'}`;
    send.disabled = !!r.error;
    summary.textContent = r.error ? r.error : confirmText(action, names, r.what);
  }
  form.addEventListener('input', refresh);
  form.addEventListener('change', refresh);

  form.addEventListener('submit', async ev => {
    ev.preventDefault();
    const r = current();
    if (r.error) return;
    send.disabled = true;
    const res = await host.api('POST', '/v1/work/bulk', r.body);
    if (res.status === 401) { close(); host.expired(); return; }
    if (!res.ok) { msg.textContent = (res.data && res.data.error) || 'request failed'; send.disabled = false; return; }
    close();
    host.done(outcome(res.data && res.data.results, nameOf));
  });

  const card = h('div', { className: 'palette bk', role: 'dialog', 'aria-modal': 'true', 'aria-label': action === 'recall' ? 'Recall units' : 'Assign a task' },
    h('h2', { textContent: action === 'recall' ? 'Recall units' : 'Assign a task' }),
    h('p', { className: 'bk-who', textContent: `${ids.length} selected (at most ${MAX_UNITS}): ${names.join(', ')}` }), form);
  const overlay = h('div', { id: 'overlay' }, card);
  overlay.addEventListener('mousedown', ev => { if (ev.target === overlay) close(); });
  document.body.append(overlay);
  refresh();
  (form.querySelector('input, select') || cancel).focus();
  return overlay;
}
