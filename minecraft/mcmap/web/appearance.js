'use strict';

// The panel where the viewer chooses how the page looks. Every control in
// it is one value of the "look" part of what the page keeps, and choosing
// one hands the changed part back to be kept: the scripts that draw hear
// of the change from there and redraw what is theirs, so this file knows
// nothing of how a theme or a size is drawn. Each control marks which of
// its values is the default, and one button puts them all back.
(() => {
  const settings = window.mcmapSettings;
  if (!settings) return;

  const el = {
    open: document.getElementById('appearance-open'),
    dialog: document.getElementById('appearance'),
    close: document.getElementById('appearance-close'),
    reset: document.getElementById('appearance-reset'),
    kept: document.getElementById('appearance-kept'),
    said: document.getElementById('appearance-said'),
    more: document.getElementById('more-open'),
  };
  if (!el.open || !el.dialog || !el.close || !el.reset || typeof el.dialog.showModal !== 'function') return;

  const DEFAULTS = settings.LOOK_DEFAULTS;
  const controls = [...el.dialog.querySelectorAll('[data-look]')].filter((node) => Object.hasOwn(DEFAULTS, node.dataset.look));

  // A control's value as the look keeps it: a number from a slider, a
  // switch from a menu of two, and otherwise the word chosen.
  const read = (node) => {
    const was = DEFAULTS[node.dataset.look];
    if (typeof was === 'number') return Number(node.value);
    if (typeof was === 'boolean') return node.value === 'true';
    return node.value;
  };

  for (const node of controls) {
    const was = DEFAULTS[node.dataset.look];
    if (node instanceof HTMLSelectElement) {
      for (const option of node.options) if (option.value === String(was)) option.textContent += ' (default)';
    }
  }

  // Brings the controls in line with what is kept, which a saved view or
  // another tab may have changed.
  function show() {
    const look = settings.get('look');
    for (const node of controls) {
      const key = node.dataset.look;
      if (node.value !== String(look[key])) node.value = String(look[key]);
      const said = node.type === 'range' ? document.getElementById(`${node.id}-said`) : null;
      if (said) said.textContent = `${look[key]}%${look[key] === DEFAULTS[key] ? ' (default)' : `, default ${DEFAULTS[key]}%`}`;
    }
    el.reset.disabled = Object.keys(DEFAULTS).every((key) => look[key] === DEFAULTS[key]);
    const kept = settings.kept();
    el.kept.hidden = kept === 'yes';
    if (kept !== 'yes') {
      el.kept.textContent = kept === 'full'
        ? 'This browser has no room left to keep settings, so these last until the page is closed.'
        : kept === 'large' ? 'What this browser keeps for the map is larger than the page ever writes and was left alone, so these last until the page is closed.'
          : 'This browser is not keeping settings, so these last until the page is closed.';
    }
  }

  for (const node of controls) {
    // A slider is followed as it is dragged, so its effect is seen on the
    // map under it.
    node.addEventListener(node.type === 'range' ? 'input' : 'change', () => {
      settings.set('look', { ...settings.get('look'), [node.dataset.look]: read(node) });
      el.said.textContent = '';
      show();
    });
  }

  el.reset.addEventListener('click', () => {
    settings.set('look', { ...DEFAULTS });
    show();
    el.said.textContent = 'Every appearance setting is back to its default.';
    el.close.focus();
  });

  // The button is hidden in the page as it is sent, so that a page whose
  // other scripts are from before there was anything to choose does not
  // offer a button that does nothing.
  el.open.hidden = false;
  el.open.addEventListener('click', () => {
    show();
    el.said.textContent = '';
    if (!el.dialog.open) el.dialog.showModal();
  });
  el.close.addEventListener('click', () => el.dialog.close());
  // A click on the backdrop is a click on the dialog itself, outside
  // everything in it.
  el.dialog.addEventListener('click', (e) => {
    if (e.target === el.dialog) el.dialog.close();
  });
  // The browser gives the focus back to the button that opened the
  // dialog, which on a small screen is in a sheet that has shut by then.
  el.dialog.addEventListener('close', () => {
    if (el.open.offsetParent === null && el.more) el.more.focus();
  });
  document.addEventListener('mcmap:settings', () => {
    if (el.dialog.open) show();
  });
})();
