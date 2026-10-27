// The alerts panel and bell. The server decides what is firing; this only
// shows it, toasts what is new and, for operators, sends acknowledge and mute.

export const RULE_LABELS = {
  unit_died: 'Unit died',
  unit_stuck: 'Unit stuck',
  task_failures: 'Repeated task failures',
  low_food: 'Low food',
  unit_silent: 'Unit silent',
};

export const MUTE_CHOICES = [['15m', '15 min'], ['1h', '1 hour'], ['8h', '8 hours']];

export const SNOOZE_CHOICES = [['15m', '15 min'], ['1h', '1 hour'], ['8h', '8 hours']];

// Active type-wide snoozes, soonest to end first, with the time left.
export function snoozeList(snoozed, now) {
  return Object.entries(snoozed || {})
    .map(([rule, until]) => ({ rule, label: ruleLabel(rule), left: Date.parse(until) - now }))
    .filter(s => s.left > 0)
    .sort((a, b) => a.left - b.left || a.rule.localeCompare(b.rule));
}

export function leftText(ms) {
  const m = Math.ceil(ms / 60000);
  return m < 60 ? `${m}m left` : `${Math.floor(m / 60)}h ${m % 60}m left`;
}

export const ruleLabel = rule => RULE_LABELS[rule] || rule;

export function isMuted(a, now) {
  return !!a.muted_until && Date.parse(a.muted_until) > now;
}

// Muted alerts stay listed but stay quiet; an acknowledged one has been seen.
export function isLoud(a, now) {
  return a.state === 'firing' && !a.acked && !isMuted(a, now);
}

export const bellCount = (list, now) => list.filter(a => isLoud(a, now)).length;

export function ageText(since, now) {
  const t = Date.parse(since);
  if (Number.isNaN(t)) return '';
  const s = Math.max(0, Math.round((now - t) / 1000));
  if (s < 60) return `${s}s`;
  if (s < 3600) return `${Math.floor(s / 60)}m`;
  return `${Math.floor(s / 3600)}h ${Math.floor((s % 3600) / 60)}m`;
}

// Alerts that began firing since the last list and are loud. Keyed on id and
// start time so a re-fire after a resolution toasts again and a repeat of the
// same list does not.
export function newlyLoud(seen, list, now) {
  const out = [];
  for (const a of list) {
    const key = `${a.id}@${a.since}`;
    if (a.state === 'firing' && !seen.has(key) && isLoud(a, now)) out.push(a);
    if (a.state === 'firing') seen.add(key);
  }
  return out;
}

const SEV_RANK = { crit: 0, warn: 1 };

// filter: { show: 'active' | 'all', severity: 'all' | 'crit' | 'warn' }
export function visible(list, filter, now) {
  return list
    .filter(a => (filter.show === 'all' || a.state === 'firing') && (filter.severity === 'all' || a.severity === filter.severity))
    .sort((x, y) => (x.state === 'firing' ? 0 : 1) - (y.state === 'firing' ? 0 : 1)
      || (SEV_RANK[x.severity] ?? 2) - (SEV_RANK[y.severity] ?? 2)
      || Date.parse(y.since) - Date.parse(x.since));
}

// host: { operator(), focus(id), api(method, path, body), setOperator(null) }
export function initAlerts(host) {
  const $ = id => document.getElementById(id);
  const el = (tag, props = {}, ...kids) => {
    const e = Object.assign(document.createElement(tag), props);
    e.append(...kids);
    return e;
  };

  let list = [];
  let snoozed = {};
  let open = false;
  let source = null;
  let initial = true;
  let message = '';
  const seen = new Set();
  const filter = { show: 'active', severity: 'all' };

  function toast(a) {
    const box = $('toasts');
    const t = el('button', { type: 'button', className: `toast ${a.severity}` }, el('b', { textContent: a.name || a.unit }), ' ', `${ruleLabel(a.rule)}: ${a.text}`);
    t.addEventListener('click', () => { t.remove(); host.focus(a.unit); });
    box.append(t);
    while (box.children.length > 4) box.firstChild.remove();
    setTimeout(() => t.remove(), a.severity === 'crit' ? 10000 : 6000);
  }

  function renderBell() {
    const n = bellCount(list, Date.now());
    const badge = $('alerts-unread');
    badge.hidden = n === 0;
    badge.textContent = String(n);
    $('alerts-toggle').setAttribute('aria-pressed', String(open));
    $('alerts-toggle').title = n ? `${n} unacknowledged alert${n === 1 ? '' : 's'} (Shift-A)` : 'Alerts (Shift-A)';
  }

  async function act(a, path, body) {
    const r = await host.api('POST', `/v1/alerts/${encodeURIComponent(a.id)}/${path}`, body);
    if (r.status === 401) host.setOperator(null);
    message = r.ok ? '' : (r.data && r.data.error) || 'request failed';
    take(r);
    render();
  }

  function take(r) {
    if (!r.ok || !r.data) return;
    if (r.data.alerts) list = r.data.alerts;
    if (r.data.snoozed) snoozed = r.data.snoozed;
  }

  async function snooze(rule, forText) {
    const r = await host.api('POST', `/v1/alerts/rules/${encodeURIComponent(rule)}/mute`, { for: forText });
    if (r.status === 401) host.setOperator(null);
    message = r.ok ? '' : (r.data && r.data.error) || 'request failed';
    take(r);
    render();
  }

  // Operators only; the server refuses anyone else, this just hides the form.
  function snoozeBox(now) {
    const type = el('select', { 'aria-label': 'Alert type to snooze' }, ...Object.entries(RULE_LABELS).map(([v, label]) => el('option', { value: v, textContent: label })));
    const dur = el('select', { 'aria-label': 'Snooze for' }, ...SNOOZE_CHOICES.map(([v, label]) => el('option', { value: v, textContent: label })));
    const go = el('button', { type: 'button', textContent: 'Snooze type', title: 'Mute every alert of this type, now and as they fire' });
    go.addEventListener('click', () => snooze(type.value, dur.value));
    const active = snoozeList(snoozed, now).map(s => {
      const lift = el('button', { type: 'button', textContent: 'Lift', title: `Stop snoozing ${s.label}` });
      lift.setAttribute('aria-label', `Lift snooze on ${s.label}`);
      lift.addEventListener('click', () => snooze(s.rule, '0s'));
      return el('li', {}, `${s.label}: ${leftText(s.left)} `, lift);
    });
    return el('div', { className: 'al-snooze' }, el('div', { className: 'al-filter' }, type, dur, go), active.length ? el('ul', { className: 'al-snoozed' }, ...active) : '');
  }

  function row(a, now) {
    const muted = isMuted(a, now);
    const li = el('li', { className: `al-row ${a.severity} ${a.state}` + (a.acked ? ' acked' : '') + (muted ? ' muted' : '') });
    const who = el('button', { type: 'button', className: 'al-unit', textContent: a.name || a.unit, title: `Focus ${a.name || a.unit}` });
    who.addEventListener('click', () => host.focus(a.unit));
    const status = a.state === 'resolved' ? 'resolved' : [a.acked ? 'acknowledged' : '', muted ? 'muted' : ''].filter(Boolean).join(', ');
    li.append(
      el('span', { className: 'al-sev', textContent: a.severity }),
      who,
      el('span', { className: 'al-text', textContent: `${ruleLabel(a.rule)}: ${a.text}` }),
      el('time', { className: 'al-age', textContent: ageText(a.since, now), title: a.since }),
    );
    if (status) li.append(el('span', { className: 'al-status', textContent: status }));
    if (host.operator() && a.state === 'firing') {
      const ack = el('button', { type: 'button', textContent: 'Ack', disabled: a.acked });
      ack.addEventListener('click', () => act(a, 'ack'));
      const sel = el('select', { 'aria-label': `Mute ${a.name || a.unit}` });
      sel.append(el('option', { value: '', textContent: muted ? 'Muted' : 'Mute...' }));
      for (const [v, label] of MUTE_CHOICES) sel.append(el('option', { value: v, textContent: label }));
      if (muted) sel.append(el('option', { value: '0s', textContent: 'Unmute' }));
      sel.addEventListener('change', () => sel.value && act(a, 'mute', { for: sel.value }));
      li.append(el('span', { className: 'al-act' }, ack, sel));
    }
    return li;
  }

  function render() {
    renderBell();
    const aside = $('alerts');
    aside.hidden = !open;
    if (!open) return;
    const now = Date.now();
    const show = el('select', { 'aria-label': 'Show' }, el('option', { value: 'active', textContent: 'firing' }), el('option', { value: 'all', textContent: 'firing and resolved' }));
    show.value = filter.show;
    show.addEventListener('change', () => { filter.show = show.value; render(); });
    const sev = el('select', { 'aria-label': 'Severity' }, el('option', { value: 'all', textContent: 'all severities' }), el('option', { value: 'crit', textContent: 'critical' }), el('option', { value: 'warn', textContent: 'warning' }));
    sev.value = filter.severity;
    sev.addEventListener('change', () => { filter.severity = sev.value; render(); });
    const rows = visible(list, filter, now);
    const out = [el('h2', { textContent: 'Alerts' }), el('div', { className: 'al-filter' }, show, sev)];
    if (message) out.push(el('p', { className: 'session-error', textContent: message }));
    if (!host.operator()) out.push(el('p', { className: 'empty', textContent: 'Sign in to acknowledge or mute.' }));
    if (host.operator()) out.push(snoozeBox(now));
    out.push(rows.length ? el('ul', { className: 'al-list' }, ...rows.map(a => row(a, now))) : el('p', { className: 'empty', textContent: 'no alerts' }));
    aside.replaceChildren(...out);
  }

  function connect() {
    if (source) return;
    const es = source = new EventSource('/v1/alerts/stream');
    es.addEventListener('alerts', ev => {
      try {
        const data = JSON.parse(ev.data) || {};
        list = data.alerts || [];
        snoozed = data.snoozed || {};
      } catch { return; }
      // The first list is state to catch up on, not news.
      const fresh = newlyLoud(seen, list, Date.now());
      if (!initial) fresh.forEach(toast);
      initial = false;
      render();
    });
    es.onerror = () => {
      if (source !== es) return;
      if (es.readyState !== EventSource.CLOSED) return;
      es.close();
      source = null;
      setTimeout(connect, 5000);
    };
  }

  function setOpen(next) {
    open = next;
    render();
  }

  // Ages and mute expiry move without a server event.
  setInterval(() => { if (open || list.length) render(); }, 5000);

  $('alerts-toggle').addEventListener('click', () => setOpen(!open));
  connect();
  render();

  return { setOpen, toggle: () => setOpen(!open), onSession: render, isOpen: () => open };
}
