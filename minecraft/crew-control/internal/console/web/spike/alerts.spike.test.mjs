// One behaviour suite, three implementations: the shipped panel, the kit
// version and the Preact version. Run: node --test spike/alerts.spike.test.mjs
import test, { describe } from 'node:test';
import assert from 'node:assert/strict';
import { installDom, settle, all, FakeEl } from '../graph/fake-dom.mjs';
import { patchDocument } from './preact-dom.mjs';

const dom = installDom();
const { doc } = dom;
patchDocument(doc);
globalThis.queueMicrotask ||= fn => Promise.resolve().then(fn);
globalThis.requestAnimationFrame = fn => setImmediate(fn);
globalThis.cancelAnimationFrame = id => clearImmediate(id);
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

const IMPLS = {
  shipped: () => import('../alerts.js'),
  kit: () => import('./alerts-kit.js'),
  preact: () => import('./alerts-preact.js'),
};

const SINCE = new Date(Date.now() - 60000).toISOString();
const el = (tag, attrs = {}) => { const e = doc.createElement(tag); for (const k in attrs) e.setAttribute(k, attrs[k]); return e; };
const al = (o = {}) => ({ id: 'unit_died.a', rule: 'unit_died', unit: 'a', name: 'Ann', text: 'died', severity: 'crit', state: 'firing', since: SINCE, acked: false, ...o });
const text = n => n.textContent;
const labelled = (root, label) => all(root, n => n.getAttribute('aria-label') === label || n['aria-label'] === label)[0];
const buttons = (root, label) => all(root, n => n.tag === 'button' && text(n) === label);

const $id = id => doc.getElementById(id);
async function boot(impl, { operator = false } = {}) {
  doc.body.children.length = 0;
  doc.activeElement = null;
  streams.length = 0;
  intervals.length = 0;
  const calls = [];
  const focused = [];
  const host = {
    operator: () => operator,
    focus: id => focused.push(id),
    api: async (method, path, body) => { calls.push({ method, path, body }); return { ok: true, status: 200, data: { alerts: [al({ acked: true })] } }; },
    setOperator: () => {},
  };
  for (const [tag, id] of [['aside', 'alerts'], ['button', 'alerts-toggle'], ['span', 'alerts-unread'], ['div', 'toasts']]) doc.body.append(el(tag, { id }));
  $id('alerts').hidden = true;
  const mod = await IMPLS[impl]();
  const api = mod.initAlerts(host);
  await settle();
  const $ = id => doc.getElementById(id);
  const push = async alerts => { streams.at(-1).emit('alerts', { alerts, snoozed: {} }); await settle(); };
  return { api, host, calls, focused, $, push };
}

for (const impl of Object.keys(IMPLS)) {
  describe(impl, () => {
    test('starts closed, opens from the toggle and lists critical first', async () => {
      const t = await boot(impl);
      assert.equal(t.$('alerts').hidden, true);
      t.$('alerts-toggle').click();
      await settle();
      assert.equal(t.$('alerts').hidden, false);
      assert.equal(t.$('alerts-toggle').getAttribute('aria-pressed'), 'true');
      await t.push([al({ id: 'w', severity: 'warn', name: 'Wes' }), al({ id: 'c' })]);
      const units = all(t.$('alerts'), n => n.className === 'al-unit').map(text);
      assert.deepEqual(units, ['Ann', 'Wes']);
    });

    test('signed out offers sign-in and no actions; operators get Ack and Mute', async () => {
      let t = await boot(impl);
      t.$('alerts-toggle').click();
      await t.push([al()]);
      assert.match(text(t.$('alerts')), /Sign in to acknowledge or mute\./);
      assert.equal(buttons(t.$('alerts'), 'Ack').length, 0);
      t = await boot(impl, { operator: true });
      t.$('alerts-toggle').click();
      await t.push([al()]);
      assert.equal(buttons(t.$('alerts'), 'Ack').length, 1);
    });

    test('ack posts to the encoded alert path and shows the answer', async () => {
      const t = await boot(impl, { operator: true });
      t.$('alerts-toggle').click();
      await t.push([al({ id: 'unit died/a' })]);
      buttons(t.$('alerts'), 'Ack')[0].click();
      await settle();
      assert.deepEqual(t.calls, [{ method: 'POST', path: '/v1/alerts/unit%20died%2Fa/ack', body: undefined }]);
      assert.equal(buttons(t.$('alerts'), 'Ack')[0].disabled, true);
    });

    test('mute posts the chosen period', async () => {
      const t = await boot(impl, { operator: true });
      t.$('alerts-toggle').click();
      await t.push([al()]);
      const sel = labelled(t.$('alerts'), 'Mute Ann');
      sel.value = '1h';
      sel.dispatch('change');
      await settle();
      assert.equal(t.calls[0].path, '/v1/alerts/unit_died.a/mute');
      assert.deepEqual(t.calls[0].body, { for: '1h' });
    });

    test('the first list is not news, a later new alert toasts once', async () => {
      const t = await boot(impl);
      await t.push([al()]);
      assert.equal(t.$('toasts').children.length, 0);
      await t.push([al(), al({ id: 'n', unit: 'b', name: 'Bo' })]);
      assert.equal(t.$('toasts').children.length, 1);
      await t.push([al(), al({ id: 'n', unit: 'b', name: 'Bo' })]);
      assert.equal(t.$('toasts').children.length, 1);
      t.$('toasts').children[0].click();
      assert.deepEqual(t.focused, ['b']);
    });

    test('the bell counts loud alerts and names them in its title', async () => {
      const t = await boot(impl);
      await t.push([al(), al({ id: 'x', acked: true })]);
      assert.equal(text(t.$('alerts-unread')), '1');
      assert.equal(t.$('alerts-toggle').title, '1 unacknowledged alert (Shift-A)');
    });

    test(impl === 'shipped' ? 'BASELINE selects have no accessible name' : 'every select has an accessible name', async () => {
      const t = await boot(impl, { operator: true });
      t.$('alerts-toggle').click();
      await t.push([al()]);
      const selects = all(t.$('alerts'), n => n.tag === 'select');
      assert.equal(selects.length, 5);
      const named = selects.filter(n => n.getAttribute('aria-label'));
      assert.equal(named.length, impl === 'shipped' ? 0 : 5);
    });

    test('empty list says so', async () => {
      const t = await boot(impl);
      t.$('alerts-toggle').click();
      await t.push([]);
      assert.match(text(t.$('alerts')), /no alerts/);
    });

    // The shipped panel redraws everything on its 5 s tick, so a focused Ack
    // button is thrown away mid-use. Recorded as the baseline, not a pass.
    test(impl === 'shipped' ? 'BASELINE LOSES focus on the 5 s tick' : 'focus survives the 5 s tick', async () => {
      const t = await boot(impl, { operator: true });
      t.$('alerts-toggle').click();
      await t.push([al()]);
      buttons(t.$('alerts'), 'Ack')[0].focus();
      assert.ok(doc.activeElement);
      for (const fn of intervals) fn();
      await settle();
      if (impl === 'shipped') assert.equal(doc.activeElement, null);
      else assert.equal(doc.activeElement && text(doc.activeElement), 'Ack');
    });
  });
}
