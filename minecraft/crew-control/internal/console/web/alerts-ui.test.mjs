import test from 'node:test';
import assert from 'node:assert/strict';
import { installDom, settle, all } from './graph/fake-dom.mjs';

const dom = installDom();
const { doc } = dom;
const intervals = [];
globalThis.setInterval = fn => { intervals.push(fn); return intervals.length; };
globalThis.clearInterval = () => {};
doc.getElementById = id => all(doc.body, n => n.attrs.id === id)[0] || null;

const streams = [];
globalThis.EventSource = class {
  static CLOSED = 2;
  constructor(url) { this.url = url; this.readyState = 1; this.l = {}; streams.push(this); }
  addEventListener(t, fn) { this.l[t] = fn; }
  close() { this.readyState = 2; }
  emit(t, data) { this.l[t]({ data: JSON.stringify(data) }); }
};
const { initAlerts } = await import('./alerts-ui.js');

const SINCE = new Date(Date.now() - 60000).toISOString();
const el = (tag, id) => { const e = doc.createElement(tag); e.setAttribute('id', id); return e; };
const al = (o = {}) => ({ id: 'unit_died.a', rule: 'unit_died', unit: 'a', name: 'Ann', text: 'died', severity: 'crit', state: 'firing', since: SINCE, acked: false, ...o });
const text = n => n.textContent;
const labelled = (root, label) => all(root, n => n.getAttribute('aria-label') === label)[0];
const buttons = (root, label) => all(root, n => n.tag === 'button' && text(n) === label);

async function boot({ operator = false, reply } = {}) {
  doc.body.replaceChildren();
  doc.activeElement = null;
  streams.length = 0;
  intervals.length = 0;
  for (const t of dom.timers.pending()) clearTimeout(t);
  const calls = [];
  const focused = [];
  const signedOut = [];
  const host = {
    operator: () => operator,
    focus: id => focused.push(id),
    api: async (method, path, body) => {
      calls.push({ method, path, body });
      return reply ? reply(path) : { ok: true, status: 200, data: { alerts: [al({ acked: true })] } };
    },
    setOperator: v => signedOut.push(v),
  };
  for (const [tag, id] of [['aside', 'alerts'], ['button', 'alerts-toggle'], ['span', 'alerts-unread'], ['div', 'toasts']]) doc.body.append(el(tag, id));
  doc.getElementById('alerts').hidden = true;
  const api = initAlerts(host);
  await settle();
  const $ = id => doc.getElementById(id);
  const push = async (alerts, snoozed = {}) => { streams.at(-1).emit('alerts', { alerts, snoozed }); await settle(); };
  const tick = () => intervals.forEach(fn => fn());
  return { api, host, calls, focused, signedOut, $, push, tick };
}

test('starts closed, opens from the toggle and lists critical first', async () => {
  const t = await boot();
  assert.equal(t.$('alerts').hidden, true);
  t.$('alerts-toggle').click();
  assert.equal(t.$('alerts').hidden, false);
  assert.equal(t.$('alerts-toggle').getAttribute('aria-pressed'), 'true');
  await t.push([al({ id: 'w', severity: 'warn', name: 'Wes' }), al({ id: 'c' })]);
  assert.deepEqual(all(t.$('alerts'), n => n.className === 'al-unit').map(text), ['Ann', 'Wes']);
});

test('shows Loading until the first list arrives', async () => {
  const t = await boot();
  t.$('alerts-toggle').click();
  assert.match(text(t.$('alerts')), /Loading\.\.\./);
  await t.push([]);
  assert.doesNotMatch(text(t.$('alerts')), /Loading/);
});

test('a stream that fails before any list shows an error with a working Retry', async () => {
  const t = await boot();
  t.$('alerts-toggle').click();
  streams[0].readyState = 0;
  streams[0].onerror();
  assert.match(text(t.$('alerts')), /Could not load alerts\. The alert stream is offline\./);
  const retry = buttons(t.$('alerts'), 'Retry')[0];
  retry.click();
  assert.equal(streams.length, 2);
  assert.equal(streams[0].readyState, 2);
  assert.match(text(t.$('alerts')), /Loading\.\.\./);
  await t.push([al()]);
  assert.match(text(t.$('alerts')), /Ann/);
});

test('an error after the list loaded does not replace the list', async () => {
  const t = await boot();
  t.$('alerts-toggle').click();
  await t.push([al()]);
  streams[0].readyState = 0;
  streams[0].onerror();
  assert.doesNotMatch(text(t.$('alerts')), /Could not load/);
  assert.match(text(t.$('alerts')), /Ann/);
});

test('the panel has a Close button that hides it', async () => {
  const t = await boot();
  t.$('alerts-toggle').click();
  await t.push([al()]);
  buttons(t.$('alerts'), 'Close')[0].click();
  assert.equal(t.$('alerts').hidden, true);
  assert.equal(t.api.isOpen(), false);
  assert.equal(t.$('alerts-toggle').getAttribute('aria-pressed'), 'false');
});

test('signed out offers sign-in and no actions; operators get Ack and Mute', async () => {
  let t = await boot();
  t.$('alerts-toggle').click();
  await t.push([al()]);
  assert.match(text(t.$('alerts')), /Sign in to acknowledge or mute\./);
  assert.equal(buttons(t.$('alerts'), 'Ack').length, 0);
  t = await boot({ operator: true });
  t.$('alerts-toggle').click();
  await t.push([al()]);
  assert.equal(buttons(t.$('alerts'), 'Ack').length, 1);
});

test('ack posts to the encoded alert path and shows the answer', async () => {
  const t = await boot({ operator: true });
  t.$('alerts-toggle').click();
  await t.push([al({ id: 'unit died/a' })]);
  buttons(t.$('alerts'), 'Ack')[0].click();
  await settle();
  assert.deepEqual(t.calls, [{ method: 'POST', path: '/v1/alerts/unit%20died%2Fa/ack', body: undefined }]);
  assert.equal(buttons(t.$('alerts'), 'Ack')[0].disabled, true);
});

test('mute posts the chosen period', async () => {
  const t = await boot({ operator: true });
  t.$('alerts-toggle').click();
  await t.push([al()]);
  const sel = labelled(t.$('alerts'), 'Mute Ann');
  sel.value = '1h';
  sel.dispatch('change');
  await settle();
  assert.equal(t.calls[0].path, '/v1/alerts/unit_died.a/mute');
  assert.deepEqual(t.calls[0].body, { for: '1h' });
});

test('snoozing a type posts the chosen type and period, and lifting posts 0s', async () => {
  const t = await boot({ operator: true, reply: () => ({ ok: true, status: 200, data: { snoozed: { low_food: new Date(Date.now() + 600000).toISOString() } } }) });
  t.$('alerts-toggle').click();
  await t.push([]);
  const type = labelled(t.$('alerts'), 'Alert type to snooze');
  type.value = 'low_food';
  type.dispatch('change');
  const dur = labelled(t.$('alerts'), 'Snooze for');
  assert.equal(dur.getAttribute('aria-label'), 'Snooze for');
  buttons(t.$('alerts'), 'Snooze type')[0].click();
  await settle();
  assert.equal(t.calls[0].path, '/v1/alerts/rules/low_food/mute');
  assert.deepEqual(t.calls[0].body, { for: '15m' });
  buttons(t.$('alerts'), 'Lift')[0].click();
  await settle();
  assert.deepEqual(t.calls[1].body, { for: '0s' });
});

test('the snooze choices survive the 5 s redraw', async () => {
  const t = await boot({ operator: true });
  t.$('alerts-toggle').click();
  await t.push([]);
  const type = labelled(t.$('alerts'), 'Alert type to snooze');
  type.value = 'low_food';
  type.dispatch('change');
  t.tick();
  await settle();
  assert.equal(labelled(t.$('alerts'), 'Alert type to snooze').value, 'low_food');
});

test('a failed action shows the reason, and a 401 signs the operator out', async () => {
  const t = await boot({ operator: true, reply: () => ({ ok: false, status: 401, data: { error: 'session expired' } }) });
  t.$('alerts-toggle').click();
  await t.push([al()]);
  buttons(t.$('alerts'), 'Ack')[0].click();
  await settle();
  assert.match(text(t.$('alerts')), /Could not update the alert\. session expired/);
  assert.deepEqual(t.signedOut, [null]);
});

test('the first list is not news, a later new alert toasts once and focuses its unit', async () => {
  const t = await boot();
  await t.push([al()]);
  assert.equal(t.$('toasts').children.length, 0);
  await t.push([al(), al({ id: 'n', unit: 'b', name: 'Bo' })]);
  assert.equal(t.$('toasts').children.length, 1);
  assert.equal(t.$('toasts').children[0].className, 'toast crit');
  await t.push([al(), al({ id: 'n', unit: 'b', name: 'Bo' })]);
  assert.equal(t.$('toasts').children.length, 1);
  t.$('toasts').children[0].click();
  assert.deepEqual(t.focused, ['b']);
});

test('the bell counts loud alerts and names them in its title', async () => {
  const t = await boot();
  await t.push([al(), al({ id: 'x', acked: true })]);
  assert.equal(text(t.$('alerts-unread')), '1');
  assert.equal(t.$('alerts-toggle').title, '1 unacknowledged alert (Shift-A)');
  await t.push([al(), al({ id: 'y', unit: 'c' })]);
  assert.equal(t.$('alerts-toggle').title, '2 unacknowledged alerts (Shift-A)');
});

test('every select has an accessible name', async () => {
  const t = await boot({ operator: true });
  t.$('alerts-toggle').click();
  await t.push([al(), al({ id: 'm', unit: 'b', name: 'Bo' })]);
  const selects = all(t.$('alerts'), n => n.tag === 'select');
  assert.equal(selects.length, 6);
  assert.equal(selects.filter(n => n.getAttribute('aria-label')).length, 6);
});

test('empty list says so', async () => {
  const t = await boot();
  t.$('alerts-toggle').click();
  await t.push([]);
  assert.match(text(t.$('alerts')), /no alerts/);
});

test('focus survives the 5 s tick', async () => {
  const t = await boot({ operator: true });
  t.$('alerts-toggle').click();
  await t.push([al()]);
  buttons(t.$('alerts'), 'Ack')[0].focus();
  t.tick();
  await settle();
  assert.equal(doc.activeElement && text(doc.activeElement), 'Ack');
});

test('focus on a select survives a live list update', async () => {
  const t = await boot({ operator: true });
  t.$('alerts-toggle').click();
  await t.push([al()]);
  labelled(t.$('alerts'), 'Mute Ann').focus();
  await t.push([al({ text: 'died again' })]);
  assert.equal(doc.activeElement.getAttribute('aria-label'), 'Mute Ann');
});

test('changing the filters redraws the list', async () => {
  const t = await boot();
  t.$('alerts-toggle').click();
  await t.push([al(), al({ id: 'r', unit: 'b', name: 'Bo', state: 'resolved' })]);
  assert.equal(all(t.$('alerts'), n => n.className === 'al-unit').length, 1);
  const show = labelled(t.$('alerts'), 'Show');
  show.value = 'all';
  show.dispatch('change');
  assert.equal(all(t.$('alerts'), n => n.className === 'al-unit').length, 2);
  const sev = labelled(t.$('alerts'), 'Severity');
  sev.value = 'warn';
  sev.dispatch('change');
  assert.equal(all(t.$('alerts'), n => n.className === 'al-unit').length, 0);
});

test('a closed stream is rebuilt after 5 s', async () => {
  await boot();
  streams[0].readyState = 2;
  streams[0].onerror();
  assert.equal(await dom.timers.fire(), 5000);
  assert.equal(streams.length, 2);
  assert.equal(streams[1].url, '/v1/alerts/stream');
});
