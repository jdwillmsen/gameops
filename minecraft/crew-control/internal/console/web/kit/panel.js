// A titled panel with an optional close button, a body that is either content
// or one of the standard states, and a live region for announcements.
import { h } from './dom.js';
import { stateView } from './states.js';

// opts: { title, label?, class?, onClose? }
export function createPanel(opts) {
  const title = h('h2', { text: opts.title });
  const head = h('header', { class: 'kit-head' }, title);
  const body = h('div', { class: 'kit-body' });
  const live = h('p', { class: 'kit-sr', role: 'status' });
  const el = h('section', { class: `kit-panel ${opts.class || ''}`.trim(), 'aria-label': opts.label || opts.title }, head, body, live);
  if (opts.onClose) {
    head.append(h('button', {
      type: 'button', class: 'kit-close', text: 'Close', 'aria-label': `Close ${opts.title}`, on: { click: opts.onClose },
    }));
  }
  const panel = {
    el, body, head, kind: 'ready',
    // show('ready', ...nodes) or show('loading' | 'empty' | 'error' | 'signedOut', options)
    show(kind, ...rest) {
      panel.kind = kind;
      el.setAttribute('aria-busy', String(kind === 'loading'));
      body.replaceChildren(...(kind === 'ready' ? rest.flat() : [stateView(kind, rest[0])]));
    },
    // Clearing first makes a repeated message announce again.
    announce(text) {
      live.textContent = '';
      live.textContent = text;
    },
    setTitle(text) {
      title.textContent = text;
    },
  };
  return panel;
}
