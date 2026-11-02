// Short-lived notices in a polite live region. The newest few stay; a click
// dismisses a toast and runs its action.
import { h } from './dom.js';

// level decides how long a toast stays: a critical one gets longer to be read.
const STAY = { crit: 10000 };
const STAY_DEFAULT = 6000;

// box is the container element; opts: { max? } (default 4).
export function createToasts(box, opts = {}) {
  const max = opts.max ?? 4;
  if (!box.getAttribute('role')) box.setAttribute('role', 'status');
  if (!box.getAttribute('aria-live')) box.setAttribute('aria-live', 'polite');

  return {
    // t: { level?, title, text, onClick?, ms? }
    show(t) {
      const level = t.level || 'info';
      const node = h('button', {
        type: 'button', class: `toast ${level}`,
        on: { click: () => { node.remove(); if (t.onClick) t.onClick(); } },
      }, h('b', { text: t.title }), ' ', t.text);
      box.append(node);
      while (box.children.length > max) box.firstChild.remove();
      setTimeout(() => node.remove(), t.ms ?? STAY[level] ?? STAY_DEFAULT);
      return node;
    },
    clear() {
      box.replaceChildren();
    },
  };
}
