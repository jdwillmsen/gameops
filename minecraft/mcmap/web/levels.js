'use strict';

// The nether's levels: a row of buttons at the top of the map, one for
// each cut of the nether the server has drawn, as an indoor map has a row
// of floors. It is there only while the nether is what is shown, with more
// than one cut to choose between and no past day on the map, which was kept
// as the default cut alone. Which cut is drawn, and that the choice is kept,
// is the map's own: this only asks it.
(() => {
  const app = window.mcmap;
  // The page and its scripts are cached apart for a few minutes, so just
  // after a release this can meet a map that has no levels to offer.
  if (!app || !app.level) return;
  const stage = document.getElementById('map');
  if (!stage) return;

  const bar = document.createElement('div');
  bar.className = 'levels';
  bar.setAttribute('role', 'group');
  bar.setAttribute('aria-label', 'Nether level');
  bar.hidden = true;
  stage.append(bar);
  // Being in the map's box, a press on it would otherwise be the map's.
  if (window.L && L.DomEvent) {
    L.DomEvent.disableClickPropagation(bar);
    L.DomEvent.disableScrollPropagation(bar);
  }

  // The offer as it was last drawn, to leave the buttons alone, and with
  // them whoever has focus on one, while it has not changed.
  let drawn = '';

  function sync() {
    const list = app.level.list();
    const same = JSON.stringify(list);
    if (list.length === 0) {
      bar.hidden = true;
      bar.replaceChildren();
      drawn = '';
      return;
    }
    if (same !== drawn) {
      drawn = same;
      bar.replaceChildren(...list.map((level) => {
        const b = document.createElement('button');
        b.type = 'button';
        b.className = 'level';
        b.dataset.id = level.id;
        b.textContent = level.label;
        b.title = `Shows the nether up to y ${level.y}`;
        b.addEventListener('click', () => app.level.set(level.id));
        return b;
      }));
    }
    const chosen = app.level.get();
    for (const b of bar.children) b.setAttribute('aria-pressed', String(b.dataset.id === chosen));
    bar.hidden = false;
  }

  document.addEventListener('mcmap:view', sync);
  sync();
})();
