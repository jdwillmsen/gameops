import { containerList, cellLabel } from '/static/containers.js';
import { iconRect, iconStyle, iconLabel, ageText } from '/static/detail.js';

function el(tag, props = {}, ...kids) {
  const e = document.createElement(tag);
  for (const [k, v] of Object.entries(props)) {
    if (k.includes('-') || k === 'role') e.setAttribute(k, v);
    else e[k] = v;
  }
  e.append(...kids);
  return e;
}

const STATE_NOTE = {
  fresh: '',
  stale: 'last seen a while ago, may have changed',
  empty: 'seen empty',
  unseen: 'no unit has looked inside yet',
};

// host: { pickUnit(id) }. The item faces come from one unit's camera sheet,
// borrowed from whichever unit last used a chest; text labels stand in until
// it answers or when none does.
export function initContainers(host) {
  const root = el('section', { id: 'containers-view', className: 'ct' });
  let sheet = null;
  let sheetUnit = '';

  async function loadSheet(id) {
    sheetUnit = id;
    try {
      const res = await fetch(`/units/${encodeURIComponent(id)}/v1/camera/items.json`, { signal: AbortSignal.timeout(4000) });
      if (!res.ok) throw new Error(String(res.status));
      sheet = { doc: await res.json(), url: `/units/${encodeURIComponent(id)}/v1/camera/items.png` };
    } catch {
      // sheetUnit stays set so a unit with no sheet is not asked again every repaint.
      sheet = null;
    }
  }

  function cell(c) {
    const label = cellLabel(c);
    const b = el('span', { className: 'dt-slot filled ct-cell', title: label });
    b.setAttribute('role', 'img');
    b.setAttribute('aria-label', label);
    const st = sheet && iconStyle(iconRect(sheet.doc, c.item));
    if (st) {
      const i = el('i', { className: 'dt-icon', 'aria-hidden': 'true' });
      for (const [k, v] of Object.entries(st)) i.style[k] = v;
      i.style.backgroundImage = `url('${sheet.url}')`;
      b.append(i);
    } else {
      b.append(el('span', { className: 'dt-noicon', 'aria-hidden': 'true', textContent: iconLabel(c.item) }));
    }
    b.append(el('span', { className: 'dt-count', textContent: String(c.count) }));
    return b;
  }

  function card(c) {
    const head = el('div', { className: 'place-head' }, el('strong', { textContent: c.name }),
      el('span', { className: 'badge', textContent: c.pos }));
    const when = c.seenAt ? `last observed ${ageText(c.seenAt)}` : 'never observed';
    const note = STATE_NOTE[c.state];
    const li = el('li', { className: `place ct-card ${c.state}` }, head,
      el('div', { className: 'ct-when', textContent: note ? `${when} - ${note}` : when }));
    li.append(c.cells.length
      ? el('div', { className: 'ct-grid' }, ...c.cells.map(cell))
      : el('div', { className: 'empty', textContent: c.state === 'unseen' ? 'contents unknown' : 'empty' }));
    if (c.units.length) {
      li.append(el('div', { className: 'ct-units' }, 'seen by ', ...c.units.map(u => {
        const b = el('button', { type: 'button', className: 'link', textContent: u, title: `Open ${u}` });
        b.addEventListener('click', () => host.pickUnit(u));
        return b;
      })));
    }
    return li;
  }

  function paint(places) {
    const list = containerList(places);
    const unit = list.flatMap(c => c.units)[0];
    if (unit && !sheet && sheetUnit !== unit) loadSheet(unit).then(() => paint(places));
    root.replaceChildren(el('ul', { id: 'container-list' },
      ...(list.length ? list.map(card) : [el('li', { className: 'empty', textContent: 'no chests' })])));
  }

  return { root, paint };
}
