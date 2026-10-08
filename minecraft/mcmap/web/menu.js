'use strict';

// The keyboard shortcuts, and the list that says what they are. Every one
// works a control the page already has, as a click on it would, so a
// shortcut can never do what the page does not offer and never differs
// from the button it stands for. They are single keys, which someone using
// speech input or a screen reader's own single keys may need out of the
// way, so one switch in the list turns them all off, and is kept.
(() => {
  const app = window.mcmap;
  if (!app || !app.map) return;
  const { map } = app;

  const el = {
    open: document.getElementById('help-open'),
    dialog: document.getElementById('help'),
    close: document.getElementById('help-close'),
    on: document.getElementById('shortcuts-on'),
    said: document.getElementById('said'),
  };
  if (!el.open || !el.dialog || !el.close || !el.on || typeof el.dialog.showModal !== 'function') return;

  // The switch is kept in the page's one record. A page from before the
  // script that keeps it has none, and the switch is then read from and
  // written to the key it always had.
  const settings = window.mcmapSettings || null;
  const KEY = 'mcmap.shortcuts';
  // How long a sentence about a shortcut that could not be done stays up.
  const SAID_MS = 5000;

  let enabled = true;
  if (settings) {
    enabled = settings.get('shortcuts').on !== false;
  } else {
    try {
      const saved = JSON.parse(localStorage.getItem(KEY) || 'null');
      if (saved && typeof saved.on === 'boolean') enabled = saved.on;
    } catch { /* a browser that refuses storage still gets the default */ }
  }
  el.on.checked = enabled;

  // A sentence over the map for a moment: why a shortcut did nothing.
  let saidTimer = null;
  function tell(text) {
    if (!el.said) return;
    clearTimeout(saidTimer);
    el.said.textContent = text;
    el.said.hidden = text === '';
    if (text !== '') saidTimer = setTimeout(() => tell(''), SAID_MS);
  }

  const press = (id) => {
    const node = document.getElementById(id);
    // Hidden or disabled is the page saying it is not on offer just now.
    if (!node || node.disabled || node.closest('[hidden]')) return false;
    node.click();
    return true;
  };

  function dimension(n) {
    const tab = document.getElementById('dimensions').children[n];
    if (!tab) return;
    if (tab.disabled) tell(`${tab.textContent} has not been rendered yet.`);
    else tab.click();
  }

  const ACTIONS = {
    '/': () => app.search && app.search.focus(),
    '?': () => show(),
    g: () => press('grid'),
    l: () => press('layers-toggle'),
    p: () => press('live-pause') || tell('This map has no live positions to pause.'),
    f: () => press('inspect-follow') || tell('Nothing is being inspected to follow: choose a player or a mob first.'),
    // The list of views, where a number then picks one.
    v: () => press('views-open'),
    1: () => dimension(0),
    2: () => dimension(1),
    3: () => dimension(2),
    s: () => app.search && app.search.jump('spawn'),
    m: () => app.search && app.search.jump('me'),
    '+': () => map.zoomIn(),
    '=': () => map.zoomIn(),
    '-': () => map.zoomOut(),
  };

  function show() {
    if (!el.dialog.open) el.dialog.showModal();
  }

  // Typing is typing: nothing here fires from a box, a menu or anything
  // else that takes text, or while a dialog has the page. A checkbox or a
  // button takes no text: with the focus left on the grid's switch or a
  // layer's, the shortcuts still work, and Space and Enter, which are not
  // among them, still press it.
  const NOT_TEXT = 'input[type="checkbox"], input[type="radio"], input[type="button"], input[type="submit"], input[type="reset"], input[type="range"], input[type="color"], input[type="file"], input[type="image"]';
  const typing = (node) => node instanceof Element
    && ((node.matches('input, textarea, select, [contenteditable]:not([contenteditable="false"])') && !node.matches(NOT_TEXT)) || node.closest('dialog[open]') !== null);

  document.addEventListener('keydown', (e) => {
    if (!enabled || e.defaultPrevented || e.ctrlKey || e.metaKey || e.altKey || e.isComposing) return;
    if (document.body.classList.contains('locked') || typing(e.target)) return;
    const key = e.key.length === 1 ? e.key.toLowerCase() : e.key;
    if (!Object.hasOwn(ACTIONS, key)) return;
    // The map zooms by these itself while it has the focus.
    if ((key === '+' || key === '=' || key === '-') && e.target === map.getContainer()) return;
    // A key held down is one press.
    if (e.repeat && key !== '+' && key !== '=' && key !== '-') return;
    e.preventDefault();
    ACTIONS[key]();
  });

  el.open.addEventListener('click', show);
  el.close.addEventListener('click', () => el.dialog.close());
  // A click on the backdrop is a click on the dialog itself, outside
  // everything in it.
  el.dialog.addEventListener('click', (e) => {
    if (e.target === el.dialog) el.dialog.close();
  });
  el.on.addEventListener('change', () => {
    enabled = el.on.checked;
    if (settings) settings.set('shortcuts', { on: enabled });
    else try { localStorage.setItem(KEY, JSON.stringify({ on: enabled })); } catch { /* not kept, still applied */ }
  });

  // The switch as it is kept may be changed from elsewhere, as by a file
  // of settings brought in.
  document.addEventListener('mcmap:settings', (e) => {
    if (!settings || !e.detail || !e.detail.sections.includes('shortcuts')) return;
    enabled = settings.get('shortcuts').on !== false;
    el.on.checked = enabled;
  });

  app.tell = tell;
})();
