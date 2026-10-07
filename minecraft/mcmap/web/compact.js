'use strict';

// The page on a small screen, where the map is given the screen and the
// controls are put away until asked for: the search behind a button that
// opens it over the bar, everything else in the bar behind More, the layer
// panel as a sheet from the edge, and the footer's timers behind one
// short reading. Only one of them is open at a time, and each is shut by
// its button, by Escape and by a touch outside it. The layout itself is
// the stylesheet's, under the same condition as here; this only keeps
// track of what is open.
(() => {
  const app = window.mcmap;
  if (!app || !app.map) return;
  const { map } = app;

  // Narrower than the bar's controls fit in two rows, or too short for
  // two rows of bar to leave a map. The stylesheet and the panel use the
  // same condition.
  const COMPACT = '(max-width: 720px), (max-height: 480px)';
  const BRIEF_MS = 500;

  const el = {
    searchOpen: document.getElementById('search-open'),
    searchClose: document.getElementById('search-close'),
    search: document.getElementById('search'),
    box: document.getElementById('search-box'),
    moreOpen: document.getElementById('more-open'),
    more: document.getElementById('more'),
    currencyOpen: document.getElementById('currency-open'),
    currency: document.getElementById('currency'),
    brief: document.getElementById('currency-brief'),
    layers: document.getElementById('layers'),
    layersToggle: document.getElementById('layers-toggle'),
    live: document.getElementById('live'),
    age: document.getElementById('live-age'),
    status: document.getElementById('status'),
  };
  if (Object.values(el).some((node) => !node)) return;

  const media = matchMedia(COMPACT);
  const body = document.body;

  // What is open, each a class on the page that the stylesheet shows it by
  // and the button that says so to a screen reader.
  const PARTS = {
    searching: el.searchOpen,
    'more-shown': el.moreOpen,
    'currency-shown': el.currencyOpen,
  };
  const shown = (part) => body.classList.contains(part);

  function set(part, on) {
    if (shown(part) === on) return;
    body.classList.toggle(part, on);
    PARTS[part].setAttribute('aria-expanded', String(on));
  }

  const panelOpen = () => el.layersToggle.getAttribute('aria-expanded') === 'true';
  function shutPanel() {
    if (panelOpen()) el.layersToggle.click();
  }

  // Opening one thing shuts the rest. The card about a mob is not shut:
  // the stylesheet tucks it away while a sheet is open and it is back
  // when the sheet goes.
  function only(part) {
    if (!media.matches) return;
    for (const other of Object.keys(PARTS)) if (other !== part) set(other, false);
    if (part !== 'panel') shutPanel();
  }

  function search(on) {
    if (!media.matches) return;
    if (on) only('searching');
    set('searching', on);
    if (on) el.box.focus();
  }

  el.searchOpen.addEventListener('click', () => search(true));
  el.searchClose.addEventListener('click', () => {
    search(false);
    el.searchOpen.focus();
  });
  // The shortcut and the list of them ask for the box by this.
  document.addEventListener('mcmap:searching', () => search(true));
  // A hit chosen, or Escape pressed twice, hands the focus to the map.
  map.getContainer().addEventListener('focusin', () => search(false));

  el.moreOpen.addEventListener('click', () => {
    const on = !shown('more-shown');
    if (on) only('more-shown');
    set('more-shown', on);
  });
  el.currencyOpen.addEventListener('click', () => {
    const on = !shown('currency-shown');
    if (on) only('currency-shown');
    set('currency-shown', on);
  });
  // The panel's own script has opened or shut it by the time this hears.
  el.layersToggle.addEventListener('click', () => {
    if (panelOpen()) only('panel');
  });
  // The card opening, and a dialog taking the page, are each the one
  // thing open.
  document.addEventListener('mcmap:inspect', (e) => {
    if (e.detail && e.detail.key) only(null);
  });
  const watcher = new MutationObserver((changes) => {
    if (changes.some((change) => change.target.open)) only(null);
  });
  for (const dialog of document.querySelectorAll('dialog')) watcher.observe(dialog, { attributes: true, attributeFilter: ['open'] });

  // A touch outside what is open shuts it. The button that opened it is
  // not outside: its own click decides.
  document.addEventListener('pointerdown', (e) => {
    if (!media.matches || !(e.target instanceof Node)) return;
    const within = (...nodes) => nodes.some((node) => node.contains(e.target));
    if (shown('more-shown') && !within(el.more, el.moreOpen)) set('more-shown', false);
    if (shown('currency-shown') && !within(el.currency, el.currencyOpen)) set('currency-shown', false);
    if (shown('searching') && !within(el.search, el.searchOpen)) set('searching', false);
    if (panelOpen() && !within(el.layers)) shutPanel();
  });

  // Escape shuts More or the timers and gives the focus back to the
  // button; heard on the way down, so that it is not also an Escape for
  // the card. The search box has its own.
  document.addEventListener('keydown', (e) => {
    if (e.key !== 'Escape' || !media.matches) return;
    // A box that takes a typed length has an Escape of its own, which
    // puts its menu back, and so has a dialog.
    if (e.target instanceof Element && e.target.closest('.duration input, dialog[open]') !== null) return;
    for (const part of ['more-shown', 'currency-shown']) {
      if (!shown(part)) continue;
      e.stopPropagation();
      set(part, false);
      PARTS[part].focus();
      return;
    }
  }, true);

  // The footer's one reading: how old the live picture is, or failing a
  // live layer, when the terrain was updated.
  function brief() {
    if (!media.matches) return;
    const text = el.live.hidden ? el.status.textContent : el.age.textContent.replace(/ old$/, '');
    if (el.brief.textContent !== text) el.brief.textContent = text;
    el.currencyOpen.classList.toggle('problem', el.status.classList.contains('problem') || /paused|reconnecting|no data/.test(text));
  }

  // Nothing stays open across a change of layout, and the map is told its
  // room has changed.
  function relaid() {
    for (const part of Object.keys(PARTS)) set(part, false);
    brief();
    map.invalidateSize();
  }

  // With no search to open there is no button for it, and logged out
  // nothing is left open over the login.
  const sync = () => {
    el.searchOpen.hidden = el.search.hidden;
    if (body.classList.contains('locked')) for (const part of Object.keys(PARTS)) set(part, false);
  };

  media.addEventListener('change', relaid);
  document.addEventListener('mcmap:view', sync);
  setInterval(brief, BRIEF_MS);
  sync();
  brief();
})();
