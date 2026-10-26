// The inventory history panel on the unit detail page: the net change over a
// window, then each recorded change. It reads an operator-only route, so a
// signed-out read says to sign in rather than showing an empty history.
import { DAY_CHOICES, netRows, entryRows, summary } from './invhistory.js';
import { ageText } from './detail.js';

const POLL_MS = 30_000;

function el(tag, props = {}, ...kids) {
  const e = document.createElement(tag);
  for (const [k, v] of Object.entries(props)) {
    if (k.includes('-') || k === 'role') e.setAttribute(k, v);
    else e[k] = v;
  }
  e.append(...kids);
  return e;
}

async function read(unit, days) {
  try {
    const res = await fetch(`/v1/units/${encodeURIComponent(unit)}/inventory/changes?days=${days}`, { cache: 'no-store' });
    if (res.status === 401) return { signedOut: true };
    return res.ok ? { report: await res.json() } : { failed: true };
  } catch {
    return { failed: true };
  }
}

export function createInventoryHistoryPanel() {
  const body = el('div', { className: 'ih-body' });
  const root = el('details', { className: 'dt-hist', open: true }, el('summary', {}, 'Inventory changes'), body);
  let unit = '';
  let days = 7;
  let seq = 0;
  let timer = 0;

  function paintError(text) {
    body.replaceChildren(el('p', { className: 'dt-none', textContent: text }));
  }

  function paint(report) {
    const picker = el('div', { className: 'ih-days', role: 'group', 'aria-label': 'Window' },
      ...DAY_CHOICES.map(d => {
        const b = el('button', { type: 'button', className: 'ih-day' + (d === days ? ' on' : ''), textContent: d === 1 ? '24 h' : `${d} d` });
        b.setAttribute('aria-pressed', String(d === days));
        b.addEventListener('click', () => { days = d; load(); });
        return b;
      }));
    const kids = [picker, el('p', { className: 'ih-sum', textContent: summary(report, days) })];
    const net = netRows(report);
    if (net.length) {
      kids.push(el('h4', { className: 'dt-sub', textContent: 'Net over the window' }),
        el('ul', { className: 'ih-net' }, ...net.map(n => el('li', {},
          el('span', { className: `ih-delta ${n.cls}`, textContent: n.text }), ` ${n.item}`))));
    }
    const entries = entryRows(report);
    if (entries.length) {
      kids.push(el('h4', { className: 'dt-sub', textContent: 'Changes, newest first' }),
        el('ul', { className: 'ih-log' }, ...entries.map(e => {
          const li = el('li', {}, el('time', { dateTime: e.at, textContent: ageText(e.at), title: new Date(e.at).toLocaleString() }), ` ${e.changes.join(', ')}`);
          return li;
        })));
    }
    body.replaceChildren(...kids);
  }

  async function load() {
    const my = ++seq;
    const r = await read(unit, days);
    if (my !== seq) return;
    if (r.signedOut) paintError('Sign in to see what this unit gained and lost.');
    else if (r.failed) paintError('The inventory history could not be read.');
    else paint(r.report);
  }

  return {
    el: root,
    start(id) {
      this.stop();
      unit = id;
      body.textContent = 'Reading…';
      load();
      timer = setInterval(() => { if (document.visibilityState === 'visible') load(); }, POLL_MS);
    },
    stop() { seq++; clearInterval(timer); timer = 0; root.remove(); },
  };
}
