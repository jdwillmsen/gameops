// Spike C: the same panel on vendored Preact + htm + hooks.
import { h, render } from './vendor/preact.mjs';
import { useState, useEffect, useRef } from './vendor/hooks.mjs';
import htm from './vendor/htm.mjs';
import {
  RULE_LABELS, MUTE_CHOICES, SNOOZE_CHOICES, ruleLabel, snoozeList, leftText, isMuted, bellCount, ageText, newlyLoud, visible,
} from '../alerts.js';

const html = htm.bind(h);
const Options = ({ pairs }) => pairs.map(([value, text]) => html`<option value=${value}>${text}</option>`);

function Row({ a, now, host, act }) {
  const muted = isMuted(a, now);
  const status = a.state === 'resolved' ? 'resolved' : [a.acked ? 'acknowledged' : '', muted ? 'muted' : ''].filter(Boolean).join(', ');
  const name = a.name || a.unit;
  return html`<li class=${`al-row ${a.severity} ${a.state}${a.acked ? ' acked' : ''}${muted ? ' muted' : ''}`}>
    <span class="al-sev">${a.severity}</span>
    <button type="button" class="al-unit" title=${`Focus ${name}`} onClick=${() => host.focus(a.unit)}>${name}</button>
    <span class="al-text">${ruleLabel(a.rule)}: ${a.text}</span>
    <time class="al-age" title=${a.since}>${ageText(a.since, now)}</time>
    ${status && html`<span class="al-status">${status}</span>`}
    ${host.operator() && a.state === 'firing' && html`<span class="al-act">
      <button type="button" disabled=${a.acked} onClick=${() => act(a, 'ack')}>Ack</button>
      <select aria-label=${`Mute ${name}`} onChange=${ev => ev.target.value && act(a, 'mute', { for: ev.target.value })}>
        <${Options} pairs=${[['', muted ? 'Muted' : 'Mute...'], ...MUTE_CHOICES, ...(muted ? [['0s', 'Unmute']] : [])]} />
      </select>
    </span>`}
  </li>`;
}

function SnoozeBox({ snoozed, now, snooze }) {
  const type = useRef(null);
  const dur = useRef(null);
  const active = snoozeList(snoozed, now);
  return html`<div class="al-snooze">
    <div class="al-filter">
      <select ref=${type} aria-label="Alert type to snooze"><${Options} pairs=${Object.entries(RULE_LABELS)} /></select>
      <select ref=${dur} aria-label="Snooze for"><${Options} pairs=${SNOOZE_CHOICES} /></select>
      <button type="button" title="Mute every alert of this type, now and as they fire" onClick=${() => snooze(type.current.value, dur.current.value)}>Snooze type</button>
    </div>
    ${active.length > 0 && html`<ul class="al-snoozed">${active.map(s => html`<li key=${s.rule}>${s.label}: ${leftText(s.left)} ${' '}
      <button type="button" title=${`Stop snoozing ${s.label}`} aria-label=${`Lift snooze on ${s.label}`} onClick=${() => snooze(s.rule, '0s')}>Lift</button></li>`)}</ul>`}
  </div>`;
}

function Alerts({ host, ctl }) {
  const [s, setS] = useState({ list: [], snoozed: {}, message: '', show: 'active', severity: 'all', loaded: false, now: Date.now(), open: false });
  const patch = p => setS(prev => ({ ...prev, ...p }));
  ctl.patch = patch;
  ctl.state = s;

  const post = async (path, body) => {
    const r = await host.api('POST', path, body);
    if (r.status === 401) host.setOperator(null);
    const data = r.ok && r.data ? r.data : {};
    patch({ message: r.ok ? '' : (r.data && r.data.error) || 'request failed', ...(data.alerts && { list: data.alerts }), ...(data.snoozed && { snoozed: data.snoozed }) });
  };
  const act = (a, path, body) => post(`/v1/alerts/${encodeURIComponent(a.id)}/${path}`, body);
  const snooze = (rule, forText) => post(`/v1/alerts/rules/${encodeURIComponent(rule)}/mute`, { for: forText });

  useEffect(() => {
    const t = setInterval(() => setS(prev => (prev.open || prev.list.length ? { ...prev, now: Date.now() } : prev)), 5000);
    return () => clearInterval(t);
  }, []);

  if (!s.open) return null;
  const now = s.now;
  const rows = visible(s.list, s, now);
  return html`<section class="kit-panel" aria-label="Alerts" aria-busy=${String(!s.loaded)}>
    <header class="kit-head"><h2>Alerts</h2><button type="button" aria-label="Close Alerts" onClick=${() => patch({ open: false })}>Close</button></header>
    <div class="kit-body">
      ${!s.loaded ? html`<p role="status">Loading...</p>` : html`
        <div class="al-filter">
          <select aria-label="Show" value=${s.show} onChange=${ev => patch({ show: ev.target.value })}><${Options} pairs=${[['active', 'firing'], ['all', 'firing and resolved']]} /></select>
          <select aria-label="Severity" value=${s.severity} onChange=${ev => patch({ severity: ev.target.value })}><${Options} pairs=${[['all', 'all severities'], ['crit', 'critical'], ['warn', 'warning']]} /></select>
        </div>
        ${s.message && html`<p class="session-error">${s.message}</p>`}
        ${host.operator() ? html`<${SnoozeBox} snoozed=${s.snoozed} now=${now} snooze=${snooze} />` : html`<p class="empty">Sign in to acknowledge or mute.</p>`}
        ${rows.length ? html`<ul class="al-list">${rows.map(a => html`<${Row} key=${a.id} a=${a} now=${now} host=${host} act=${act} />`)}</ul>` : html`<p class="empty">no alerts</p>`}`}
    </div>
  </section>`;
}

export function initAlerts(host) {
  const $ = id => document.getElementById(id);
  const ctl = { patch: null, state: null };
  const seen = new Set();
  let source = null;
  let initial = true;
  let open = false;
  let list = [];

  const draw = () => render(html`<${Alerts} host=${host} ctl=${ctl} />`, $('alerts'));
  function bell() {
    const n = bellCount(list, Date.now());
    $('alerts-unread').hidden = n === 0;
    $('alerts-unread').textContent = String(n);
    $('alerts-toggle').setAttribute('aria-pressed', String(open));
    $('alerts-toggle').title = n ? `${n} unacknowledged alert${n === 1 ? '' : 's'} (Shift-A)` : 'Alerts (Shift-A)';
  }
  function toast(a) {
    const box = $('toasts');
    const t = document.createElement('button');
    t.type = 'button';
    t.className = `toast ${a.severity}`;
    t.append(Object.assign(document.createElement('b'), { textContent: a.name || a.unit }), ` ${ruleLabel(a.rule)}: ${a.text}`);
    t.addEventListener('click', () => { t.remove(); host.focus(a.unit); });
    box.append(t);
    while (box.children.length > 4) box.firstChild.remove();
    setTimeout(() => t.remove(), a.severity === 'crit' ? 10000 : 6000);
  }
  function connect() {
    if (source) return;
    const es = source = new EventSource('/v1/alerts/stream');
    es.addEventListener('alerts', ev => {
      let data;
      try { data = JSON.parse(ev.data) || {}; } catch { return; }
      list = data.alerts || [];
      const fresh = newlyLoud(seen, list, Date.now());
      if (!initial) fresh.forEach(toast);
      initial = false;
      ctl.patch({ list, snoozed: data.snoozed || {}, loaded: true });
      bell();
    });
    es.onerror = () => {
      if (source !== es || es.readyState !== EventSource.CLOSED) return;
      es.close();
      source = null;
      setTimeout(connect, 5000);
    };
  }
  const setOpen = next => { open = next; $('alerts').hidden = !open; ctl.patch({ open }); bell(); };
  $('alerts-toggle').addEventListener('click', () => setOpen(!open));
  draw();
  connect();
  bell();
  return { setOpen, toggle: () => setOpen(!open), onSession: draw, isOpen: () => open };
}
