// The four non-data states every view paints (docs/ui-design-principles.md),
// built once so the wording and the roles cannot drift between views.
import { h } from './dom.js';

export const SIGN_IN_HREF = '/signin';

// "Could not load alerts. Failed (500)." from a failed api() result.
export function errorText(verb, res) {
  const why = (res && res.data && res.data.error) || `Failed (${res ? res.status : 0}).`;
  return `Could not ${verb}. ${why}`;
}

function need(kind, o, key) {
  if (typeof o[key] !== 'string' || o[key] === '') throw new Error(`state ${kind} needs ${key}`);
  return o[key];
}

// kind: 'loading' | 'empty' | 'error' | 'signedOut'
export function stateView(kind, o = {}) {
  switch (kind) {
    case 'loading':
      return h('p', { class: 'kit-state kit-loading', role: 'status', text: 'Loading...' });
    case 'empty':
      return h('p', { class: 'kit-state kit-empty', text: need('empty', o, 'message') });
    case 'error':
      return h('div', { class: 'kit-state kit-error', role: 'alert' },
        h('p', { text: need('error', o, 'message') }),
        o.retry && h('button', { type: 'button', 'data-focus-key': 'kit-retry', text: 'Retry', on: { click: o.retry } }));
    case 'signedOut':
      return h('div', { class: 'kit-state kit-signed-out' },
        h('h3', { text: `Sign in to ${need('signedOut', o, 'what')}` }),
        h('a', { href: SIGN_IN_HREF, text: 'Sign in' }));
    default:
      throw new Error(`unknown state ${kind}`);
  }
}
