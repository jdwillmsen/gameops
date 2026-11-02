// A yes/no question in the kit dialog, standing in for window.confirm, which
// blocks the page (and the live feed with it) until it is answered.
import { h } from './dom.js';
import { openDialog } from './dialog.js';

// opts: { label, message, confirmLabel? }. Resolves true only for the confirm
// button; Cancel, Escape and a click on the backdrop all resolve false.
export function confirmDialog(opts) {
  return new Promise(resolve => {
    let answer = false;
    const cancel = h('button', { type: 'button', text: 'Cancel', on: { click: () => dialog.close() } });
    const ok = h('button', { type: 'button', class: 'danger', text: opts.confirmLabel || 'Confirm', on: { click: () => { answer = true; dialog.close(); } } });
    // The safe answer holds focus so a stray Enter cannot destroy anything.
    const dialog = openDialog({
      label: opts.label, class: 'confirm',
      content: [h('h2', { text: opts.label }), h('p', { text: opts.message }), h('div', { class: 'confirm-actions' }, cancel, ok)],
      focus: cancel,
      onClose: () => resolve(answer),
    });
  });
}
