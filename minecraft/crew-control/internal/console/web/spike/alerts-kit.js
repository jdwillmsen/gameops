// Spike B: the alerts panel on the in-house kit (h, panel, states, store).
import {
  RULE_LABELS, MUTE_CHOICES, SNOOZE_CHOICES, ruleLabel, snoozeList, leftText, isMuted, bellCount, ageText, newlyLoud, visible,
} from '../alerts.js';
import { h, keepFocus } from '../kit/dom.js';
import { createPanel } from '../kit/panel.js';
import { createStore } from '../kit/store.js';
import { errorText } from '../kit/states.js';

const opts = pairs => pairs.map(([value, text]) => h('option', { value, text }));

export function initAlerts(host) {
  const $ = id => document.getElementById(id);
  const store = createStore({ list: [], snoozed: {}, open: false, message: '', show: 'active', severity: 'all', loaded: false, now: Date.now() });
  const panel = createPanel({ title: 'Alerts', onClose: () => store.set({ open: false }) });
  $('alerts').replaceChildren(panel.el);
  const seen = new Set();
  let source = null;
  let initial = true;

  function toast(a) {
    const box = $('toasts');
    const t = h('button', { type: 'button', class: `toast ${a.severity}`, on: { click: () => { t.remove(); host.focus(a.unit); } } },
      h('b', { text: a.name || a.unit }), ` ${ruleLabel(a.rule)}: ${a.text}`);
    box.append(t);
    while (box.children.length > 4) box.firstChild.remove();
    setTimeout(() => t.remove(), a.severity === 'crit' ? 10000 : 6000);
  }

  async function post(path, body) {
    const r = await host.api('POST', path, body);
    if (r.status === 401) host.setOperator(null);
    const data = r.ok && r.data ? r.data : {};
    store.set({ message: r.ok ? '' : errorText('update the alert', r), ...(data.alerts && { list: data.alerts }), ...(data.snoozed && { snoozed: data.snoozed }) });
  }
  const act = (a, path, body) => post(`/v1/alerts/${encodeURIComponent(a.id)}/${path}`, body);
  const snooze = (rule, forText) => post(`/v1/alerts/rules/${encodeURIComponent(rule)}/mute`, { for: forText });

  const select = (label, options, value, onChange, key) =>
    h('select', { 'aria-label': label, value, [`data-focus-key`]: key, on: { change: ev => onChange(ev.target.value) } }, ...opts(options));

  function snoozeBox({ snoozed, now }) {
    const type = select('Alert type to snooze', Object.entries(RULE_LABELS), 'unit_died', () => {}, 'snooze-type');
    const dur = select('Snooze for', SNOOZE_CHOICES, '15m', () => {}, 'snooze-for');
    const active = snoozeList(snoozed, now).map(s => h('li', {}, `${s.label}: ${leftText(s.left)} `,
      h('button', { type: 'button', text: 'Lift', title: `Stop snoozing ${s.label}`, 'aria-label': `Lift snooze on ${s.label}`, on: { click: () => snooze(s.rule, '0s') } })));
    return h('div', { class: 'al-snooze' },
      h('div', { class: 'al-filter' }, type, dur,
        h('button', { type: 'button', text: 'Snooze type', title: 'Mute every alert of this type, now and as they fire', on: { click: () => snooze(type.value, dur.value) } })),
      active.length > 0 && h('ul', { class: 'al-snoozed' }, ...active));
  }

  function row(a, now) {
    const muted = isMuted(a, now);
    const status = a.state === 'resolved' ? 'resolved' : [a.acked ? 'acknowledged' : '', muted ? 'muted' : ''].filter(Boolean).join(', ');
    const sel = h('select', { 'aria-label': `Mute ${a.name || a.unit}`, 'data-focus-key': `mute-${a.id}`, on: { change: ev => ev.target.value && act(a, 'mute', { for: ev.target.value }) } },
      ...opts([['', muted ? 'Muted' : 'Mute...'], ...MUTE_CHOICES, ...(muted ? [['0s', 'Unmute']] : [])]));
    return h('li', { class: `al-row ${a.severity} ${a.state}${a.acked ? ' acked' : ''}${muted ? ' muted' : ''}` },
      h('span', { class: 'al-sev', text: a.severity }),
      h('button', { type: 'button', class: 'al-unit', text: a.name || a.unit, title: `Focus ${a.name || a.unit}`, on: { click: () => host.focus(a.unit) } }),
      h('span', { class: 'al-text', text: `${ruleLabel(a.rule)}: ${a.text}` }),
      h('time', { class: 'al-age', text: ageText(a.since, now), title: a.since }),
      status && h('span', { class: 'al-status', text: status }),
      host.operator() && a.state === 'firing' && h('span', { class: 'al-act' },
        h('button', { type: 'button', text: 'Ack', disabled: a.acked, 'data-focus-key': `ack-${a.id}`, on: { click: () => act(a, 'ack') } }), sel));
  }

  function renderBell({ list, open }) {
    const n = bellCount(list, Date.now());
    $('alerts-unread').hidden = n === 0;
    $('alerts-unread').textContent = String(n);
    $('alerts-toggle').setAttribute('aria-pressed', String(open));
    $('alerts-toggle').title = n ? `${n} unacknowledged alert${n === 1 ? '' : 's'} (Shift-A)` : 'Alerts (Shift-A)';
  }

  function render(s) {
    renderBell(s);
    panel.el.parentNode.hidden = !s.open;
    if (!s.open) return;
    const rows = visible(s.list, s, s.now);
    keepFocus(panel.el, () => {
      if (!s.loaded) return panel.show('loading');
      panel.show('ready',
        h('div', { class: 'al-filter' },
          select('Show', [['active', 'firing'], ['all', 'firing and resolved']], s.show, v => store.set({ show: v }), 'show'),
          select('Severity', [['all', 'all severities'], ['crit', 'critical'], ['warn', 'warning']], s.severity, v => store.set({ severity: v }), 'severity')),
        s.message && h('p', { class: 'session-error', text: s.message }),
        host.operator() ? snoozeBox(s) : h('p', { class: 'empty', text: 'Sign in to acknowledge or mute.' }),
        rows.length ? h('ul', { class: 'al-list' }, ...rows.map(a => row(a, s.now))) : h('p', { class: 'empty', text: 'no alerts' }));
    });
  }
  store.subscribe(render);

  function connect() {
    if (source) return;
    const es = source = new EventSource('/v1/alerts/stream');
    es.addEventListener('alerts', ev => {
      let data;
      try { data = JSON.parse(ev.data) || {}; } catch { return; }
      const list = data.alerts || [];
      const fresh = newlyLoud(seen, list, Date.now());
      if (!initial) fresh.forEach(toast);
      initial = false;
      store.set({ list, snoozed: data.snoozed || {}, loaded: true });
    });
    es.onerror = () => {
      if (source !== es || es.readyState !== EventSource.CLOSED) return;
      es.close();
      source = null;
      setTimeout(connect, 5000);
    };
  }

  const setOpen = open => store.set({ open });
  setInterval(() => { const s = store.get(); if (s.open || s.list.length) store.set({ now: Date.now() }); }, 5000);
  $('alerts-toggle').addEventListener('click', () => setOpen(!store.get().open));
  connect();
  render(store.get());
  return { setOpen, toggle: () => setOpen(!store.get().open), onSession: () => render(store.get()), isOpen: () => store.get().open };
}
