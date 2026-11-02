// A tab strip with a roving tabindex: one Tab stop for the whole strip, arrows
// move and select, and the caller shows the matching panel from onSelect.
import { h } from './dom.js';

// opts: { label, tabs: [{ id, label }], selected?, onSelect(id), panelId?(id) }
export function createTabs(opts) {
  let current = opts.selected ?? opts.tabs[0].id;
  const buttons = new Map();

  function paint() {
    for (const [id, b] of buttons) {
      const on = id === current;
      b.setAttribute('aria-selected', String(on));
      b.tabIndex = on ? 0 : -1;
      b.classList.toggle('selected', on);
    }
  }

  function select(id, focus) {
    if (!buttons.has(id)) return;
    const changed = id !== current;
    current = id;
    paint();
    if (focus) buttons.get(id).focus();
    if (changed && opts.onSelect) opts.onSelect(id);
  }

  const ids = opts.tabs.map(t => t.id);
  function onKey(ev, id) {
    const at = ids.indexOf(id);
    let next;
    if (ev.key === 'ArrowRight') next = ids[(at + 1) % ids.length];
    else if (ev.key === 'ArrowLeft') next = ids[(at + ids.length - 1) % ids.length];
    else if (ev.key === 'Home') next = ids[0];
    else if (ev.key === 'End') next = ids[ids.length - 1];
    else return;
    ev.preventDefault();
    select(next, true);
  }

  const el = h('div', { class: 'kit-tabs', role: 'tablist', 'aria-label': opts.label });
  for (const t of opts.tabs) {
    const b = h('button', {
      type: 'button', class: 'kit-tab', role: 'tab', text: t.label, 'data-tab': t.id,
      'aria-controls': opts.panelId ? opts.panelId(t.id) : null,
      on: { click: () => select(t.id, false), keydown: ev => onKey(ev, t.id) },
    });
    buttons.set(t.id, b);
    el.append(b);
  }
  paint();

  return { el, select: id => select(id, false), selected: () => current };
}
