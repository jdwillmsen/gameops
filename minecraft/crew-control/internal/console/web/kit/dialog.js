// A modal: the card takes focus, Tab stays inside it, Escape and a click on the
// backdrop close it, and focus goes back to whatever opened it.
import { h } from './dom.js';

const FOCUSABLE = ['a', 'button', 'input', 'select', 'textarea', '[tabindex]'];

function isFocusable(n) {
  if (n.disabled || n.tabIndex < 0) return false;
  return FOCUSABLE.some(sel => n.matches(sel)) && n.type !== 'hidden';
}

// Document order; a hidden subtree is skipped because it cannot take focus.
export function focusables(root) {
  const out = [];
  const walk = n => {
    for (const c of Array.from(n.children || [])) {
      if (typeof c.matches !== 'function' || c.hidden) continue;
      if (isFocusable(c)) out.push(c);
      walk(c);
    }
  };
  walk(root);
  return out;
}

// opts: { label, class?, content: nodes, focus?: element to focus first, onClose? }
// Returns { el, card, close }. The overlay keeps id "overlay" because the
// global key handler asks whether one is open.
export function openDialog(opts) {
  const opener = document.activeElement;
  let done = false;

  const card = h('div', {
    class: `palette ${opts.class || ''}`.trim(), role: 'dialog', 'aria-modal': 'true', 'aria-label': opts.label, tabIndex: -1,
  }, opts.content);
  const el = h('div', { id: 'overlay' }, card);

  function close() {
    if (done) return;
    done = true;
    el.remove();
    if (opener && opener.isConnected && typeof opener.focus === 'function') opener.focus();
    if (opts.onClose) opts.onClose();
  }

  el.addEventListener('mousedown', ev => { if (ev.target === el) close(); });
  el.addEventListener('keydown', ev => {
    if (ev.key === 'Escape') {
      ev.preventDefault();
      // The global handler would close the same dialog a second time.
      ev.stopPropagation();
      close();
      return;
    }
    if (ev.key !== 'Tab') return;
    const items = focusables(card);
    if (!items.length) {
      ev.preventDefault();
      card.focus();
      return;
    }
    const first = items[0];
    const last = items[items.length - 1];
    const at = document.activeElement;
    if (!card.contains(at) || at === card) {
      ev.preventDefault();
      (ev.shiftKey ? last : first).focus();
    } else if (ev.shiftKey && at === first) {
      ev.preventDefault();
      last.focus();
    } else if (!ev.shiftKey && at === last) {
      ev.preventDefault();
      first.focus();
    }
  });

  document.body.append(el);
  (opts.focus || focusables(card)[0] || card).focus();
  return { el, card, close };
}
