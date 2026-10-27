// The bulk recall / assign dialog. It shows what will happen to which units
// and only then offers the send button, which names the count.
import { buildRequest, confirmText, outcome, MAX_UNITS } from './bulk.js';
import { GATHER_ITEMS } from './state.js';

const el = (tag, props = {}, ...kids) => {
  const e = Object.assign(document.createElement(tag), props);
  e.append(...kids);
  return e;
};

// host: { api(method, path, body), name(id), expired(), done(outcome) }
// Returns the overlay element so the caller's Esc handling can remove it.
export function openBulk(host, action, ids, close) {
  const names = ids.map(host.name);
  const nameOf = Object.fromEntries(ids.map((id, i) => [id, names[i]]));
  const field = (label, input) => {
    const l = el('label', { className: 'bk-field' }, el('span', { textContent: label }), input);
    return l;
  };
  const num = (label, v) => field(label, el('input', { type: 'text', inputMode: 'numeric', value: v, size: 5, autocomplete: 'off' }));
  const point = (title, init) => {
    const x = num(`${title} x`, init[0]), y = num(`${title} y`, init[1]), z = num(`${title} z`, init[2]);
    const read = () => ({ x: x.querySelector('input').value, y: y.querySelector('input').value, z: z.querySelector('input').value });
    return { nodes: [x, y, z], read };
  };

  const form = el('form', { className: 'bk-form' });
  const to = point('Destination', ['', '', '']);
  const chest = point('Chest', ['', '', '']);
  const from = point('From', ['', '', '']);
  const template = el('select', { 'aria-label': 'Task' }, el('option', { value: 'gather', textContent: 'Gather and deposit' }), el('option', { value: 'haul', textContent: 'Haul between chests' }));
  const item = el('select', { 'aria-label': 'Item' }, ...GATHER_ITEMS.map(i => el('option', { value: i, textContent: i })));
  const qty = el('input', { type: 'text', inputMode: 'numeric', value: '16', size: 4, autocomplete: 'off' });
  qty.setAttribute('aria-label', 'Quantity');
  const fromRow = el('div', { className: 'bk-row' }, ...from.nodes);

  if (action === 'recall') form.append(el('div', { className: 'bk-row' }, ...to.nodes));
  else form.append(el('div', { className: 'bk-row' }, template, item, qty), el('div', { className: 'bk-row' }, ...chest.nodes), fromRow);

  const summary = el('p', { className: 'bk-summary', role: 'status' });
  const msg = el('p', { className: 'session-error', role: 'alert' });
  const send = el('button', { type: 'submit', className: 'danger' });
  const cancel = el('button', { type: 'button', textContent: 'Cancel' });
  cancel.addEventListener('click', close);
  form.append(summary, msg, el('div', { className: 'bk-row' }, send, cancel));

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

  const card = el('div', { className: 'palette bk', role: 'dialog' },
    el('h2', { textContent: action === 'recall' ? 'Recall units' : 'Assign a task' }),
    el('p', { className: 'bk-who', textContent: `${ids.length} selected (at most ${MAX_UNITS}): ${names.join(', ')}` }), form);
  card.setAttribute('aria-modal', 'true');
  card.setAttribute('aria-label', action === 'recall' ? 'Recall units' : 'Assign a task');
  const overlay = el('div', { id: 'overlay' }, card);
  overlay.addEventListener('mousedown', ev => { if (ev.target === overlay) close(); });
  document.body.append(overlay);
  refresh();
  (form.querySelector('input, select') || cancel).focus();
  return overlay;
}
