import { h } from './kit/dom.js';
// The squad panel is a view: it shows the public snapshot and can open a
// member's camera. Orders are issued from the operator panel, never here.
import { openStream } from './kit/stream.js';

const ROLE_LABELS = { vanguard: 'Vanguard', striker: 'Striker', ranger: 'Ranger', mender: 'Mender', builder: 'Builder' };

export const roleLabel = role => ROLE_LABELS[role] || role || 'member';

// The role a unit's own status reports, or '' for a unit with none, so a tile
// or row shows a badge only when there is something to say.
export const statusRole = s => (s && ROLE_LABELS[s.role] ? roleLabel(s.role) : '');

// The server sends callouts newest first, which is also the order the panel
// logs them in.
export function lastCallout(callouts, unit) {
  return (callouts || []).find(c => c.from === unit) || null;
}

export function orderLine(order) {
  if (!order) return 'no order';
  return order.target ? `${order.kind} ${order.target}` : order.kind;
}

// host: { statuses, focus(id) }
export function initSquads(host) {
  const $ = id => document.getElementById(id);


  let open = false;
  let data = { squads: [], battalions: [] };
  let source = null;

  const nameOf = unit => {
    const s = host.statuses && host.statuses.get(unit);
    return (s && s.name) || unit;
  };

  const clock = at => {
    const t = Date.parse(at);
    return Number.isNaN(t) ? '' : new Date(t).toLocaleTimeString([], { hour12: false });
  };

  function meter(value, max, kind) {
    const known = typeof value === 'number';
    const b = h('span', { className: `meter ${kind}` + (known && value <= 6 ? ' low' : '') });
    b.title = known ? `${Math.round(value)} / ${max}` : 'unknown';
    b.append(h('i'));
    b.firstChild.style.width = (known ? Math.max(0, Math.min(100, (value / max) * 100)) : 0) + '%';
    return b;
  }

  function memberRow(sq, m) {
    const dead = m.alive === false;
    const last = lastCallout(sq.callouts, m.unit);
    const row = h('button', {
      type: 'button',
      className: 'sq-member' + (dead ? ' dead' : ''),
      title: `Focus ${nameOf(m.unit)}`,
    });
    if (sq.leader === m.unit) row.append(h('span', { className: 'crown', textContent: '♛', title: 'Leader' }));
    row.append(
      h('span', { className: 'sq-name', textContent: nameOf(m.unit) }),
      h('span', { className: 'role ' + m.role, textContent: roleLabel(m.role) }),
      h('span', { className: 'sq-hp' }, meter(m.hp, 20, 'hp')),
      h('span', { className: 'sq-food', textContent: `food ${m.food ?? '-'}` }),
      h('span', { className: 'sq-weapon', textContent: m.weapon || 'no weapon' }),
      h('span', { className: 'sq-callout' + (last ? ' on' : ''), textContent: last ? last.kind : 'no callout' }),
    );
    row.addEventListener('click', () => host.focus(m.unit));
    return row;
  }

  function squadBox(sq) {
    const box = h('section', { className: 'sq' });
    const head = h('h3', {}, h('span', { textContent: sq.name || sq.id }));
    if (sq.battalion) head.append(h('span', { className: 'sq-bn', textContent: sq.battalion }));
    box.append(head);
    box.append(h('div', { className: 'sq-order' },
      h('span', { textContent: 'Order ' }), h('b', { textContent: orderLine(sq.order) })));

    const members = h('div', { className: 'sq-members' });
    for (const m of sq.members || []) members.append(memberRow(sq, m));
    box.append(members);

    const calls = sq.callouts || [];
    if (calls.length) {
      const log = h('ul', { className: 'sq-log' }, ...calls.slice(0, 8).map(c => h('li', {},
        h('time', { textContent: clock(c.at) }),
        h('b', { textContent: nameOf(c.from) }),
        h('span', { textContent: c.kind }))));
      box.append(h('h4', { textContent: 'Callouts' }), log);
    }
    return box;
  }

  function render() {
    const aside = $('squad');
    $('squad-toggle').setAttribute('aria-pressed', String(open));
    aside.hidden = !open;
    if (!open) return;
    const out = [h('h2', { textContent: 'Squads' })];
    if (!data.squads.length) out.push(h('p', { className: 'empty', textContent: 'no squads yet' }));
    for (const sq of data.squads) out.push(squadBox(sq));
    aside.replaceChildren(...out);
  }

  function connect() {
    if (source || !open) return;
    source = openStream('/v1/squads/stream', {
      squads(ev) {
        try {
          const d = JSON.parse(ev.data) || {};
          data = { squads: d.squads || [], battalions: d.battalions || [] };
        } catch { return; }
        render();
      },
    }, { min: 5000, max: 5000 });
  }

  function setOpen(next) {
    open = next;
    if (!open && source) {
      source.close();
      source = null;
    }
    render();
    if (open) connect();
  }

  render();

  return { setOpen, onUnits: render, isOpen: () => open };
}
