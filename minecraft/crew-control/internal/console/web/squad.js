// The squad panel is a view: it shows the public snapshot and can open a
// member's camera. Orders are issued from the operator panel, never here.

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

  const el = (tag, props = {}, ...kids) => {
    const e = Object.assign(document.createElement(tag), props);
    e.append(...kids);
    return e;
  };

  let open = false;
  let data = { squads: [], battalions: [] };
  let source = null;
  let retry = null;

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
    const b = el('span', { className: `meter ${kind}` + (known && value <= 6 ? ' low' : '') });
    b.title = known ? `${Math.round(value)} / ${max}` : 'unknown';
    b.append(el('i'));
    b.firstChild.style.width = (known ? Math.max(0, Math.min(100, (value / max) * 100)) : 0) + '%';
    return b;
  }

  function memberRow(sq, m) {
    const dead = m.alive === false;
    const last = lastCallout(sq.callouts, m.unit);
    const row = el('button', {
      type: 'button',
      className: 'sq-member' + (dead ? ' dead' : ''),
      title: `Focus ${nameOf(m.unit)}`,
    });
    if (sq.leader === m.unit) row.append(el('span', { className: 'crown', textContent: '♛', title: 'Leader' }));
    row.append(
      el('span', { className: 'sq-name', textContent: nameOf(m.unit) }),
      el('span', { className: 'role ' + m.role, textContent: roleLabel(m.role) }),
      el('span', { className: 'sq-hp' }, meter(m.hp, 20, 'hp')),
      el('span', { className: 'sq-food', textContent: `food ${m.food ?? '-'}` }),
      el('span', { className: 'sq-weapon', textContent: m.weapon || 'no weapon' }),
      el('span', { className: 'sq-callout' + (last ? ' on' : ''), textContent: last ? last.kind : 'no callout' }),
    );
    row.addEventListener('click', () => host.focus(m.unit));
    return row;
  }

  function squadBox(sq) {
    const box = el('section', { className: 'sq' });
    const head = el('h3', {}, el('span', { textContent: sq.name || sq.id }));
    if (sq.battalion) head.append(el('span', { className: 'sq-bn', textContent: sq.battalion }));
    box.append(head);
    box.append(el('div', { className: 'sq-order' },
      el('span', { textContent: 'Order ' }), el('b', { textContent: orderLine(sq.order) })));

    const members = el('div', { className: 'sq-members' });
    for (const m of sq.members || []) members.append(memberRow(sq, m));
    box.append(members);

    const calls = sq.callouts || [];
    if (calls.length) {
      const log = el('ul', { className: 'sq-log' }, ...calls.slice(0, 8).map(c => el('li', {},
        el('time', { textContent: clock(c.at) }),
        el('b', { textContent: nameOf(c.from) }),
        el('span', { textContent: c.kind }))));
      box.append(el('h4', { textContent: 'Callouts' }), log);
    }
    return box;
  }

  function render() {
    const aside = $('squad');
    $('squad-toggle').setAttribute('aria-pressed', String(open));
    aside.hidden = !open;
    if (!open) return;
    const out = [el('h2', { textContent: 'Squads' })];
    if (!data.squads.length) out.push(el('p', { className: 'empty', textContent: 'no squads yet' }));
    for (const sq of data.squads) out.push(squadBox(sq));
    aside.replaceChildren(...out);
  }

  function connect() {
    if (source || !open) return;
    const es = source = new EventSource('/v1/squads/stream');
    es.addEventListener('squads', ev => {
      try {
        const d = JSON.parse(ev.data) || {};
        data = { squads: d.squads || [], battalions: d.battalions || [] };
      } catch { return; }
      render();
    });
    es.onerror = () => {
      if (source !== es) return;
      // The browser retries a live stream itself; a closed one (a gateway
      // restart or the stream cap) is rebuilt while the panel is open.
      if (es.readyState !== EventSource.CLOSED) return;
      es.close();
      source = null;
      if (open) retry = setTimeout(connect, 5000);
    };
  }

  function setOpen(next) {
    open = next;
    if (!open) {
      clearTimeout(retry);
      if (source) {
        source.close();
        source = null;
      }
    }
    render();
    if (open) connect();
  }

  render();

  return { setOpen, onUnits: render, isOpen: () => open };
}
