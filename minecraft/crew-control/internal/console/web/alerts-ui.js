// The alerts panel and bell. alerts.js holds the logic; the server decides what
// is firing and this shows it, toasts what is new and, for operators, sends
// acknowledge and mute.
import {
  RULE_LABELS, MUTE_CHOICES, SNOOZE_CHOICES, ruleLabel, snoozeList, leftText, isMuted, bellCount, ageText, newlyLoud, visible,
} from './alerts.js';
import { h, keepFocus } from './kit/dom.js';
import { createPanel } from './kit/panel.js';
import { createStore } from './kit/store.js';
import { errorText } from './kit/states.js';
import { openStream } from './kit/stream.js';
import { createToasts } from './kit/toast.js';

const opts = pairs => pairs.map(([value, text]) => h('option', { value, text }));

// An unlabelled select cannot be told apart by a screen reader, so every one
// goes through here and must name itself.
const select = (label, options, value, onChange, key) =>
  h('select', { 'aria-label': label, value, 'data-focus-key': key, on: { change: ev => onChange(ev.target.value) } }, ...opts(options));

// host: { operator(), focus(id), api(method, path, body), setOperator(null) }
export function initAlerts(host) {
  const $ = id => document.getElementById(id);
  const store = createStore({
    list: [], snoozed: {}, open: false, message: '', show: 'active', severity: 'all', loaded: false, failed: false,
    snoozeRule: 'unit_died', snoozeFor: '15m', now: Date.now(),
  });
  const panel = createPanel({ title: 'Alerts', label: 'Alert list', onClose: () => setOpen(false) });
  $('alerts').replaceChildren(panel.el);
  const toasts = createToasts($('toasts'));
  const seen = new Set();
  let stream = null;
  let initial = true;

  const toast = a => toasts.show({
    level: a.severity, title: a.name || a.unit, text: `${ruleLabel(a.rule)}: ${a.text}`, onClick: () => host.focus(a.unit),
  });

  async function post(path, body) {
    const r = await host.api('POST', path, body);
    if (r.status === 401) host.setOperator(null);
    const data = r.ok && r.data ? r.data : {};
    store.set({ message: r.ok ? '' : errorText('update the alert', r), ...(data.alerts && { list: data.alerts }), ...(data.snoozed && { snoozed: data.snoozed }) });
  }
  const act = (a, path, body) => post(`/v1/alerts/${encodeURIComponent(a.id)}/${path}`, body);
  const snooze = (rule, forText) => post(`/v1/alerts/rules/${encodeURIComponent(rule)}/mute`, { for: forText });

  // Operators only; the server refuses anyone else, this just hides the form.
  function snoozeBox({ snoozed, now, snoozeRule, snoozeFor }) {
    const type = select('Alert type to snooze', Object.entries(RULE_LABELS), snoozeRule, v => store.set({ snoozeRule: v }), 'snooze-type');
    const dur = select('Snooze for', SNOOZE_CHOICES, snoozeFor, v => store.set({ snoozeFor: v }), 'snooze-for');
    const active = snoozeList(snoozed, now).map(s => h('li', {}, `${s.label}: ${leftText(s.left)} `,
      h('button', {
        type: 'button', text: 'Lift', title: `Stop snoozing ${s.label}`, 'aria-label': `Lift snooze on ${s.label}`, 'data-focus-key': `lift-${s.rule}`, on: { click: () => snooze(s.rule, '0s') },
      })));
    return h('div', { class: 'al-snooze' },
      h('div', { class: 'al-filter' }, type, dur,
        h('button', {
          type: 'button', text: 'Snooze type', title: 'Mute every alert of this type, now and as they fire', 'data-focus-key': 'snooze-go', on: { click: () => snooze(type.value, dur.value) },
        })),
      active.length > 0 && h('ul', { class: 'al-snoozed' }, ...active));
  }

  function row(a, now) {
    const muted = isMuted(a, now);
    const status = a.state === 'resolved' ? 'resolved' : [a.acked ? 'acknowledged' : '', muted ? 'muted' : ''].filter(Boolean).join(', ');
    const mute = h('select', {
      'aria-label': `Mute ${a.name || a.unit}`, 'data-focus-key': `mute-${a.id}`,
      on: { change: ev => ev.target.value && act(a, 'mute', { for: ev.target.value }) },
    }, ...opts([['', muted ? 'Muted' : 'Mute...'], ...MUTE_CHOICES, ...(muted ? [['0s', 'Unmute']] : [])]));
    return h('li', { class: `al-row ${a.severity} ${a.state}${a.acked ? ' acked' : ''}${muted ? ' muted' : ''}` },
      h('span', { class: 'al-sev', text: a.severity }),
      h('button', { type: 'button', class: 'al-unit', text: a.name || a.unit, title: `Focus ${a.name || a.unit}`, on: { click: () => host.focus(a.unit) } }),
      h('span', { class: 'al-text', text: `${ruleLabel(a.rule)}: ${a.text}` }),
      h('time', { class: 'al-age', text: ageText(a.since, now), title: a.since }),
      status && h('span', { class: 'al-status', text: status }),
      host.operator() && a.state === 'firing' && h('span', { class: 'al-act' },
        h('button', { type: 'button', text: 'Ack', disabled: a.acked, 'data-focus-key': `ack-${a.id}`, on: { click: () => act(a, 'ack') } }), mute));
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
    $('alerts').hidden = !s.open;
    if (!s.open) return;
    // The page redraws every few seconds; focus must come back to the same control.
    keepFocus(panel.el, () => {
      if (!s.loaded) {
        if (s.failed) panel.show('error', { message: 'Could not load alerts. The alert stream is offline.', retry: reconnect });
        else panel.show('loading');
        return;
      }
      const rows = visible(s.list, s, s.now);
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
    stream = openStream('/v1/alerts/stream', {
      alerts(ev) {
        let data;
        try { data = JSON.parse(ev.data) || {}; } catch { return; }
        const list = data.alerts || [];
        // The first list is state to catch up on, not news.
        const fresh = newlyLoud(seen, list, Date.now());
        if (!initial) fresh.forEach(toast);
        initial = false;
        store.set({ list, snoozed: data.snoozed || {}, loaded: true, failed: false });
      },
    }, {
      min: 5000,
      max: 5000,
      onError() { if (!store.get().loaded) store.set({ failed: true }); },
    });
  }

  function reconnect() {
    stream.close();
    store.set({ failed: false });
    connect();
  }

  const setOpen = open => store.set({ open });
  // Ages and mute expiry move without a server event.
  setInterval(() => { const s = store.get(); if (s.open || s.list.length) store.set({ now: Date.now() }); }, 5000);
  $('alerts-toggle').addEventListener('click', () => setOpen(!store.get().open));
  connect();
  render(store.get());

  return { setOpen, toggle: () => setOpen(!store.get().open), onSession: () => render(store.get()), isOpen: () => store.get().open };
}
