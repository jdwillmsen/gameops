'use strict';

// Saved views. A view is a named snapshot of what the map shows and how:
// which layers are on, which mobs and players are filtered out, the biome
// picked out, the trails' window, the grid, the live layer's pace, and, if
// it was saved with them, the place and the appearance settings. Switching
// to one puts all of it in place at once. Three come with the page, and
// the rest are the viewer's own, kept in the browser with everything else
// the page keeps.
//
// A view can be given to someone else as a link or in a file. Both are
// read as what they are, something a stranger wrote: against a closed
// description, with every name shown as text and never as anything else,
// and neither is ever put in the place of a view the viewer saved.
(() => {
  const app = window.mcmap;
  // The page and its scripts are cached apart for a few minutes, so just
  // after a release this can meet scripts from before there were views.
  const settings = window.mcmapSettings;
  if (!app || !app.map || !settings || !app.place || !app.layers || !app.layers.adopt) return;

  const el = {
    open: document.getElementById('views-open'),
    dialog: document.getElementById('views'),
    close: document.getElementById('views-close'),
    kept: document.getElementById('views-kept'),
    list: document.getElementById('views-list'),
    said: document.getElementById('views-said-text'),
    undo: document.getElementById('views-undo'),
    form: document.getElementById('views-save'),
    formTitle: document.getElementById('views-save-title'),
    name: document.getElementById('views-name'),
    place: document.getElementById('views-place'),
    look: document.getElementById('views-look'),
    hidden: document.getElementById('views-hidden'),
    hiddenList: document.getElementById('views-hidden-list'),
    offer: document.getElementById('views-offer'),
    offerName: document.getElementById('views-offer-name'),
    offerWhat: document.getElementById('views-offer-what'),
    offerShow: document.getElementById('views-offer-show'),
    offerSave: document.getElementById('views-offer-save'),
    offerDrop: document.getElementById('views-offer-drop'),
    exportAll: document.getElementById('views-export'),
    importAll: document.getElementById('views-import'),
    file: document.getElementById('views-file'),
    incoming: document.getElementById('views-incoming'),
    incomingWhat: document.getElementById('views-incoming-what'),
    incomingTake: document.getElementById('views-incoming-take'),
    incomingDrop: document.getElementById('views-incoming-drop'),
    grid: document.getElementById('grid'),
    more: document.getElementById('more-open'),
    preview: document.getElementById('preview'),
    previewName: document.getElementById('preview-name'),
    previewKeep: document.getElementById('preview-keep'),
    previewBack: document.getElementById('preview-back'),
  };
  if (Object.values(el).some((node) => !node) || typeof el.dialog.showModal !== 'function') return;

  // The layers the page has had, by the key each one's choice is kept
  // under, with what its row is called. A link says which of these are on
  // by their place in this list, so it is only ever added to at the end.
  const KNOWN = [
    ['live/players', 'Players'], ['live/hostile', 'Hostile'], ['live/passive', 'Passive'], ['live/villager', 'Villagers'], ['live/other', 'Other'],
    ['markers/waypoints', 'Waypoints'], ['markers/beds', 'Beds'], ['markers/containers', 'Containers'], ['markers/mobs', 'Named mobs'],
    ['structures/recorded', 'Known structures'], ['structures/predicted', 'Predicted structures'], ['structures/candidate', 'Possible structures'],
    ['structures/fortress', 'Fortresses'], ['structures/monument', 'Monuments'], ['structures/outpost', 'Outposts'],
    ['structures/witch_hut', 'Witch huts'], ['structures/village', 'Villages'], ['structures/spawn', 'World spawn'],
    ['biomes/overlay', 'Biome overlay'], ['overlays/slime', 'Slime chunks'], ['overlays/trails', 'Trails'],
    ['overlays/grid', 'Grid'], ['overlays/chunk', 'Chunk focus'],
  ];
  // The switches a kind of structure had before each layer listed its
  // kinds. A view saved then names them, and what they said is put on the
  // lists by the script that keeps the record: they are not layers this
  // map lacks.
  const RETIRED = new Set(settings.RETIRED || []);
  // The choices over what a layer is made of that a link carries, by
  // their place in this list, which is only ever added to at the end.
  // Each is over things the game names. Which players, named mobs and
  // waypoints are hidden is left out: those are not the viewer's to hand
  // round in a link.
  const SHARED = ['structures#kinds', 'markers#containers', 'markers#beds', 'biomes#items', 'live#mobs'];
  const KEYS = KNOWN.map(([key]) => key);
  const LABELS = new Map(KNOWN);
  // The appearance settings in the order a link lists them, likewise only
  // ever added to at the end.
  const LOOKS = ['theme', 'size', 'text', 'labelMobs', 'labelPlayers', 'labelWaypoints', 'picturesLive', 'picturesMarkers',
    'opacityBiomes', 'opacityTrails', 'opacitySlime', 'density', 'motion', 'coords', 'style', 'mobPicture'];
  // The most a link's view may come to, in characters: short enough to
  // paste into a chat, and far more than any view of this page's needs.
  const MAX_LINK = 1800;
  const MAX_FILE = 300_000;
  const FILE_NAME = 'mcmap-settings.json';

  // Why nothing is being kept, for the states in which nothing is.
  const UNKEPT = {
    no: 'This browser is not keeping settings',
    full: 'This browser has no room left to keep anything',
    newer: 'What this browser keeps for the map was written by a later version of the page, and is left as it is',
    large: 'What this browser keeps for the map is larger than the page ever writes, so it was left alone and the page is on its defaults',
  };

  const make = (tag, className, text) => {
    const node = document.createElement(tag);
    if (className) node.className = className;
    // Always as text: a view's name is the viewer's, or a stranger's.
    if (text !== undefined) node.textContent = text;
    return node;
  };
  const plural = (n, one, many) => `${n.toLocaleString('en-US')} ${n === 1 ? one : many}`;
  const dimensionName = (id) => (app.label ? app.label(id) : id);

  // A view that came in a link, until it is saved or dismissed: never
  // kept, and never one of the viewer's own.
  let offered = null;
  // Whether the form is naming the offered view and not the map as it is.
  let naming = false;
  // The view last deleted, for as long as it can be put back.
  let undone = null;
  // Something to say as soon as there is a map to say it over.
  let pending = settings.kept() === 'large' ? `${UNKEPT.large}. Nothing new will be kept until the site’s data is cleared.` : '';
  // What the map was showing when a preview of a link's view began: where
  // it was, what was pinned and who was followed. Null while there is no
  // preview.
  let before = null;
  // Whether the list has been opened on the offered view yet.
  let announced = true;

  // --- taking and applying --------------------------------------------------

  // What the map shows now, as a view without a name.
  function capture(withPlace, withLook) {
    const kept = settings.get('layers');
    const layers = {};
    for (const [key, on] of Object.entries(kept)) if (key.includes('/') && typeof on === 'boolean' && !RETIRED.has(key)) layers[key] = on;
    Object.assign(layers, app.layers.states ? app.layers.states() : {});
    const filter = (domain) => {
      const f = kept[`live#${domain}`];
      return { only: f && typeof f.only === 'string' ? f.only : null, hidden: f && Array.isArray(f.hidden) ? f.hidden.filter((sort) => typeof sort === 'string') : [] };
    };
    const view = {
      layers,
      mobs: filter('mobs'),
      players: filter('players'),
      biome: settings.get('biome').only,
      trails: settings.get('trails').seconds,
      interval: settings.get('live').interval,
      grid: el.grid.checked,
    };
    // Which of what each layer is made of are shown, every choice there
    // is: a view says "all of it" as well as what is hidden.
    if (app.layers.items) view.items = app.layers.items();
    const at = withPlace ? app.place.get() : null;
    if (at) {
      view.place = at;
      const pin = app.chunk ? app.chunk.pinned() : null;
      if (pin) view.place.pin = { u: pin.unit, x: pin.x, z: pin.z };
    }
    if (withLook) view.look = settings.get('look');
    return view;
  }

  // Brings each script that draws in line with what is now kept, in the
  // order that has each redraw once: the filters and windows first, which
  // draw nothing by themselves, then every row at a stroke.
  function redraw(layers) {
    if (app.inspect && app.inspect.adopt) app.inspect.adopt();
    if (app.trails && app.trails.adopt) app.trails.adopt();
    const held = app.biomes && app.biomes.adopt ? app.biomes.adopt() : true;
    const missing = app.layers.adopt(layers);
    if (app.inspect && app.inspect.settle) app.inspect.settle();
    const grid = settings.get('grid').on;
    if (el.grid.checked !== grid) el.grid.click();
    return { held, missing };
  }

  // Switches the map to a view, and gives back what of it could not be
  // done, in words: a view may be older than the page, or from a server
  // that offers more than this one does. Everything here happens in one
  // turn of the page, so nothing is drawn half-way between two views.
  // temporary is for a view that is only being previewed: what is kept is
  // held as it is first, and nothing done here is written.
  function apply(view, temporary) {
    const skipped = [];
    const next = { ...view };
    const knows = app.inspect && app.inspect.knows ? app.inspect.knows : null;
    if (next.mobs && knows) {
      const named = [next.mobs.only, ...next.mobs.hidden].filter((type) => type !== null);
      const gone = named.filter((type) => !knows(type));
      if (gone.length > 0) {
        next.mobs = { only: next.mobs.only !== null && knows(next.mobs.only) ? next.mobs.only : null, hidden: next.mobs.hidden.filter(knows) };
        skipped.push(`${plural(gone.length, 'type of mob', 'types of mob')} this map does not know (${gone.slice(0, 3).join(', ')}${gone.length > 3 ? ', …' : ''})`);
      }
    }
    // Asked before anything is touched: a view the record has no room for
    // is refused whole, with the map where it was.
    if (!settings.fits(next)) return null;
    if (temporary) settings.hold();
    // The place first: another dimension starts every layer afresh, and
    // what the view says of them is then applied to that. Whoever is being
    // followed is let go of before the map is moved, or the next frame
    // would bring it straight back to them; a view with no place of its
    // own leaves Follow as it is.
    let unfollowed = '';
    if (next.place) {
      const follow = app.inspect && app.inspect.follow ? app.inspect.follow : null;
      unfollowed = follow ? follow(false) : '';
      if (!app.place.set(next.place)) {
        skipped.push(`its place, since ${dimensionName(next.place.d)} is not there to show`);
        if (unfollowed !== '') follow(true);
        unfollowed = '';
      }
    }
    settings.adopt(next);
    const { held, missing } = redraw(Object.keys(next.layers).filter((key) => !RETIRED.has(key)));
    if (next.place && next.place.pin && app.chunk && app.chunk.pin && app.place.get() && app.place.get().d === next.place.d) {
      app.chunk.pin({ unit: next.place.pin.u, x: next.place.pin.x, z: next.place.pin.z });
    }
    if (!held && next.biome) skipped.push('the biome it picks out, which this dimension does not hold');
    if (missing.length > 0) {
      const names = missing.map((key) => LABELS.get(key) || key);
      skipped.push(`${plural(missing.length, 'layer', 'layers')} this map does not have (${names.slice(0, 4).join(', ')}${names.length > 4 ? ', …' : ''})`);
    }
    return { skipped, unfollowed };
  }

  const tell = (text) => {
    if (app.tell) app.tell(text);
  };

  // What is said over the map of a view just shown: what of it was left
  // out, and that Follow is off if its place turned it off.
  const outcome = (verb, view, { skipped, unfollowed }) => `${verb} “${view.name}”${skipped.length > 0 ? `, without ${skipped.join('; ')}` : ''}.${unfollowed !== '' ? ` No longer following ${unfollowed}: the view has a place of its own.` : ''}`;
  const TOO_LARGE = 'That view could not be shown: it would make what the page keeps too large.';

  function switchTo(view) {
    if (!settings.fits(view)) {
      say(TOO_LARGE);
      return;
    }
    // Chosen while a link's view is being previewed: the preview is let go
    // of without the map being drawn as it was only to be changed again.
    if (before) leave(true);
    const done = apply(view);
    if (done === null) {
      say(TOO_LARGE);
      return;
    }
    if (el.dialog.open) el.dialog.close();
    tell(outcome('Showing', view, done));
  }

  // --- previewing a view from a link -------------------------------------------
  //
  // A link's view is shown without being kept. The record is held as it
  // was, so nothing the preview changes is written and a reload is the
  // viewer's own setup again; a bar over the map says so for as long as
  // it lasts, with a way to keep the view and a way back.

  function preview(view) {
    if (!settings.fits(view)) {
      say(TOO_LARGE);
      return;
    }
    if (!before) {
      const centre = app.map.getCenter();
      before = {
        d: app.dimension(),
        x: centre.lng,
        z: centre.lat,
        zoom: app.map.getZoom(),
        pin: app.chunk ? app.chunk.pinned() : null,
        follow: app.inspect && app.inspect.following ? app.inspect.following() : null,
      };
    }
    el.previewName.textContent = view.name;
    el.preview.hidden = false;
    // The bar takes its room from the map.
    app.map.invalidateSize();
    const done = apply(view, true);
    if (el.dialog.open) el.dialog.close();
    tell(outcome('Previewing', view, done));
    el.previewBack.focus();
  }

  // Ends a preview and puts back exactly what was there: what is kept,
  // the place to the fraction of a block, the pin, and who was followed.
  // quiet is for when another view is about to be shown in its place.
  function leave(quiet) {
    const was = before;
    if (!was) return;
    before = null;
    const within = el.preview.contains(document.activeElement);
    el.preview.hidden = true;
    app.map.invalidateSize();
    if (quiet) {
      settings.release();
      return;
    }
    // The place while the record is still held: another dimension lets go
    // of the biome picked out, and that must not be what is kept.
    if (was.d) app.place.set({ d: was.d, x: was.x, z: was.z, zoom: was.zoom });
    settings.release();
    redraw([]);
    if (app.chunk && app.chunk.pin) app.chunk.pin(was.pin);
    if (was.follow && app.inspect && app.inspect.key() === was.follow) app.inspect.follow(true);
    tell('The map is as it was before the shared view.');
    if (within) app.map.getContainer().focus();
  }

  // --- the viewer's list ------------------------------------------------------

  const newId = () => {
    const taken = new Set(settings.get('views').order);
    for (;;) {
      const id = `v${Math.random().toString(36).slice(2, 12)}`;
      if (/^[a-z0-9]{2,24}$/.test(id) && !taken.has(id)) return id;
    }
  };

  function say(text, undoable) {
    el.said.textContent = text;
    el.undo.hidden = !undoable;
    if (!undoable) undone = null;
  }

  // Hands a changed list back to be kept, and says so if it would not fit.
  function keep(views, done) {
    if (!settings.set('views', views)) {
      say('That is more than the page keeps: delete a view first, or export them to a file.');
      return false;
    }
    say(done || '');
    return true;
  }

  function describe(view) {
    if (view.says) return view.says;
    const on = Object.values(view.layers).filter(Boolean).length;
    const parts = [`${plural(on, 'layer', 'layers')} on`];
    if (view.place) parts.push(`a place in ${dimensionName(view.place.d)}`);
    if (view.look) parts.push('its own appearance');
    return parts.join(', ');
  }

  // The focus is put back on the control that was used, which the list
  // being drawn again would otherwise have taken away with the old row.
  let refocus = null;
  let expanded = null;
  let renaming = null;

  function row(view, n, views, shown) {
    const builtIn = settings.BUILT_IN.includes(view.id);
    const item = make('li', 'view');
    const current = views.active === view.id;
    if (current) item.setAttribute('aria-current', 'true');

    const pick = make('button', 'view-pick');
    pick.type = 'button';
    pick.dataset.id = view.id;
    pick.dataset.act = 'pick';
    if (n <= 9) pick.append(make('kbd', '', String(n)));
    const title = make('span', 'view-title');
    title.append(make('span', 'name', view.name));
    const notes = [describe(view)];
    if (builtIn) notes.unshift('Built in');
    if (views.start === view.id) notes.push('opens with the page');
    if (current) notes.push('showing now');
    title.append(make('span', 'what', notes.join(' · ')));
    // Said where it is marked, since it undoes what the viewer changes.
    if (views.start === view.id) {
      title.append(make('span', 'what', `Every load puts back its layers, filters and overlays${view.place ? ', and its place' : ''}. The appearance stays as you last set it.`));
    }
    pick.append(title);
    pick.addEventListener('click', () => switchTo(view));

    const more = make('button', 'mini', 'Edit');
    more.type = 'button';
    more.dataset.id = view.id;
    more.dataset.act = 'more';
    more.setAttribute('aria-label', `Edit ${view.name}`);
    more.setAttribute('aria-expanded', String(expanded === view.id));
    const actions = make('div', 'view-actions');
    actions.id = `view-actions-${n}`;
    more.setAttribute('aria-controls', actions.id);
    actions.hidden = expanded !== view.id;
    more.addEventListener('click', () => {
      expanded = expanded === view.id ? null : view.id;
      renaming = null;
      refocus = { id: view.id, act: 'more' };
      render();
    });

    const act = (label, name, fn, disabled) => {
      const button = make('button', 'mini', label);
      button.type = 'button';
      button.dataset.id = view.id;
      button.dataset.act = name;
      button.disabled = Boolean(disabled);
      button.addEventListener('click', () => {
        refocus = { id: view.id, act: name };
        fn();
        render();
      });
      actions.append(button);
    };
    const at = shown.indexOf(view.id);
    if (!builtIn) {
      act('Rename', 'rename', () => { renaming = view.id; });
      act('Update to what the map shows', 'update', () => update(view.id));
    }
    act('Move up', 'up', () => move(view.id, -1), at === 0);
    act('Move down', 'down', () => move(view.id, 1), at === shown.length - 1);
    act(views.start === view.id ? 'Do not open with the page' : 'Open with the page', 'start', () => start(view.id));
    act('Copy link to this view', 'link', () => copyLink(view));
    if (builtIn) act('Hide', 'hide', () => hide(view.id, true));
    else act('Delete', 'delete', () => remove(view.id));

    item.append(pick, more, actions);
    if (renaming === view.id) {
      const form = make('form', 'view-rename');
      form.autocomplete = 'off';
      const label = make('label', '', 'New name ');
      const box = make('input');
      box.type = 'text';
      box.maxLength = settings.NAME_LENGTH;
      box.required = true;
      box.value = view.name;
      box.dataset.id = view.id;
      box.dataset.act = 'rename-box';
      label.append(box);
      const save = make('button', 'mini', 'Save name');
      save.type = 'submit';
      const cancel = make('button', 'mini', 'Cancel');
      cancel.type = 'button';
      form.append(label, save, cancel);
      form.addEventListener('submit', (e) => {
        e.preventDefault();
        if (!rename(view.id, box.value)) return;
        renaming = null;
        refocus = { id: view.id, act: 'rename' };
        render();
      });
      cancel.addEventListener('click', () => {
        renaming = null;
        refocus = { id: view.id, act: 'rename' };
        render();
      });
      actions.append(form);
      refocus = refocus && refocus.act === 'rename' && refocus.id === view.id ? { id: view.id, act: 'rename-box' } : refocus;
    }
    return item;
  }

  function render() {
    const views = settings.get('views');
    const all = views.order.map((id) => settings.view(id)).filter(Boolean);
    const shown = all.filter((view) => !views.hidden.includes(view.id));
    const ids = shown.map((view) => view.id);
    el.list.replaceChildren(...shown.map((view, i) => row(view, i + 1, views, ids)));

    const put = all.filter((view) => views.hidden.includes(view.id));
    el.hidden.hidden = put.length === 0;
    el.hiddenList.replaceChildren(...put.map((view) => {
      const item = make('li', 'view');
      const back = make('button', 'mini', 'Show again');
      back.type = 'button';
      back.dataset.id = view.id;
      back.dataset.act = 'unhide';
      back.setAttribute('aria-label', `Show ${view.name} again`);
      back.addEventListener('click', () => {
        hide(view.id, false);
        refocus = { id: view.id, act: 'more' };
        render();
      });
      item.append(make('span', 'name', view.name), back);
      return item;
    }));

    el.offer.hidden = offered === null;
    if (offered !== null) {
      el.offerName.textContent = offered.name;
      el.offerWhat.textContent = describe(offered);
    }
    el.formTitle.textContent = naming ? 'Save the view from the link' : 'Save what the map shows now';
    el.place.closest('label').hidden = naming;
    el.look.closest('label').hidden = naming;

    const kept = settings.kept();
    el.kept.hidden = kept === 'yes';
    if (kept !== 'yes') el.kept.textContent = `${UNKEPT[kept] || UNKEPT.no}, so views saved here last until the page is closed. Export them to a file to keep them.`;

    if (refocus) {
      const node = [...el.dialog.querySelectorAll('[data-act]')].find((other) => other.dataset.id === refocus.id && other.dataset.act === refocus.act && !other.disabled && !other.closest('[hidden]'))
        || [...el.dialog.querySelectorAll('[data-act="more"]')].find((other) => other.dataset.id === refocus.id)
        || el.close;
      refocus = null;
      node.focus();
      if (node instanceof HTMLInputElement) node.select();
    }
  }

  function add(view, name) {
    const clean = settings.check.name(name);
    if (clean === null) {
      say('A view needs a name: up to 40 letters, with nothing in it but what can be read.');
      return null;
    }
    const views = settings.get('views');
    if (views.list.length >= settings.MAX_VIEWS) {
      say(`The page keeps up to ${settings.MAX_VIEWS} views. Delete one first.`);
      return null;
    }
    const id = newId();
    views.list.push({ ...view, id, name: clean });
    views.order.push(id);
    return keep(views, `Saved “${clean}”.`) ? id : null;
  }

  function rename(id, name) {
    const clean = settings.check.name(name);
    if (clean === null) {
      say('A view needs a name: up to 40 letters, with nothing in it but what can be read.');
      return false;
    }
    const views = settings.get('views');
    const view = views.list.find((other) => other.id === id);
    if (!view) return false;
    view.name = clean;
    return keep(views, `Renamed to “${clean}”.`);
  }

  // The view keeps its name and its place in the list, and takes what the
  // map shows now, with a place and an appearance if it had them before.
  function update(id) {
    const views = settings.get('views');
    const at = views.list.findIndex((other) => other.id === id);
    if (at < 0) return;
    const was = views.list[at];
    views.list[at] = { ...capture(Boolean(was.place), Boolean(was.look)), id, name: was.name };
    views.active = id;
    keep(views, `“${was.name}” now holds what the map shows.`);
  }

  function move(id, by) {
    const views = settings.get('views');
    const shown = views.order.filter((other) => !views.hidden.includes(other));
    const from = shown.indexOf(id);
    const to = from + by;
    if (from < 0 || to < 0 || to >= shown.length) return;
    const a = views.order.indexOf(id);
    const b = views.order.indexOf(shown[to]);
    [views.order[a], views.order[b]] = [views.order[b], views.order[a]];
    keep(views);
  }

  function start(id) {
    const views = settings.get('views');
    const on = views.start !== id;
    views.start = on ? id : null;
    const view = settings.view(id);
    keep(views, on ? `“${view.name}” is what the page will open with: its layers, filters and overlays${view.place ? ', and its place,' : ''} are put back on every load. The appearance is not.` : 'The page will open as it was left.');
  }

  function hide(id, on) {
    const views = settings.get('views');
    views.hidden = on ? [...views.hidden, id] : views.hidden.filter((other) => other !== id);
    expanded = null;
    keep(views, on ? `“${settings.view(id).name}” is hidden. It can be shown again from the list below.` : '');
  }

  function remove(id) {
    const views = settings.get('views');
    const at = views.list.findIndex((other) => other.id === id);
    if (at < 0) return;
    const [view] = views.list.splice(at, 1);
    const kept = { view, at, order: views.order.indexOf(id), start: views.start === id };
    views.order = views.order.filter((other) => other !== id);
    expanded = null;
    if (!keep(views)) return;
    undone = kept;
    say(`Deleted “${view.name}”.`, true);
    refocus = { id: '', act: 'undo' };
  }

  function restore() {
    if (!undone) return;
    const { view, at, order, start: wasStart } = undone;
    const views = settings.get('views');
    if (views.list.some((other) => other.id === view.id)) return;
    views.list.splice(Math.min(at, views.list.length), 0, view);
    views.order.splice(Math.min(order, views.order.length), 0, view.id);
    if (wasStart) views.start = view.id;
    if (keep(views, `“${view.name}” is back.`)) refocus = { id: view.id, act: 'more' };
    render();
  }

  // --- a view in a link -------------------------------------------------------
  //
  // The address is <dimension>/<x>/<z>/<zoom>/<what is open>/<view>: the
  // view is one more part after the ones the page already had, so an
  // address without one reads exactly as before, and a page from before
  // views reads an address with one as far as it understands. The view is
  // "v1." and then a small JSON object, as base64 in the letters an
  // address may carry. A view saved with its place puts that place in
  // the first parts; one saved without says "-", which is no dimension,
  // so whoever opens it stays where their own page would start.

  const b64 = {
    to(text) {
      let raw = '';
      for (const byte of new TextEncoder().encode(text)) raw += String.fromCharCode(byte);
      return btoa(raw).replace(/\+/g, '-').replace(/\//g, '_').replace(/=+$/, '');
    },
    from(packed) {
      const raw = atob(packed.replace(/-/g, '+').replace(/_/g, '/'));
      return new TextDecoder('utf-8', { fatal: true }).decode(Uint8Array.from(raw, (c) => c.charCodeAt(0)));
    },
  };

  function pack(view) {
    const out = { n: view.name, l: KEYS.map((key) => (view.layers[key] === true ? '1' : view.layers[key] === false ? '0' : '-')).join('') };
    const others = Object.entries(view.layers).filter(([key]) => !KEYS.includes(key));
    if (others.length > 0) out.x = Object.fromEntries(others.map(([key, on]) => [key, on ? 1 : 0]));
    // Which players are hidden is left out: a gamertag is not the
    // viewer's to hand round in a link.
    if (view.mobs) out.m = { o: view.mobs.only, h: view.mobs.hidden };
    if (view.items) {
      // Each choice that is not "all of it", under its place in the list.
      // That the view has choices at all is said even when none is.
      out.y = {};
      SHARED.forEach((key, at) => {
        const f = view.items[key];
        if (f && (f.only !== null || f.mode === 'just' || f.hidden.length > 0 || (f.shown && f.shown.length > 0))) out.y[at] = { o: f.only, h: f.hidden, ...(f.shown ? { s: f.shown } : {}), ...(f.mode === 'just' ? { j: f.just } : {}) };
      });
    }
    if (view.biome !== undefined) out.b = view.biome;
    if (view.trails !== undefined) out.t = view.trails;
    if (view.interval !== undefined) out.i = view.interval;
    if (view.grid !== undefined) out.g = view.grid ? 1 : 0;
    if (view.place) {
      out.p = [view.place.d, view.place.x, view.place.z, view.place.zoom];
      if (view.place.pin) out.q = [view.place.pin.u, view.place.pin.x, view.place.pin.z];
    }
    // Each appearance setting the view has, under its place in the list: a
    // view may have only some of them, and one it lacks is left out.
    const look = {};
    LOOKS.forEach((key, at) => {
      if (view.look && view.look[key] !== undefined) look[at] = view.look[key];
    });
    if (Object.keys(look).length > 0) out.a = look;
    return `v1.${b64.to(JSON.stringify(out))}`;
  }

  // Reads the view out of a link's part: { view } or { error }. Every key
  // and every value is checked here for its form and then, as a view, by
  // the same strict description a file is read against. The error is a
  // sentence of this page's own; nothing from the link is ever part of it.
  function unpack(part) {
    const bad = { error: 'it was not written the way this page writes one' };
    if (typeof part !== 'string' || part === '') return bad;
    if (part.length > MAX_LINK) return { error: 'it was longer than a link’s view may be' };
    if (!/^v1\.[A-Za-z0-9_-]+$/.test(part)) return bad;
    let raw;
    try { raw = JSON.parse(b64.from(part.slice(3))); } catch { return bad; }
    if (raw === null || typeof raw !== 'object' || Array.isArray(raw)) return bad;
    const allowed = ['n', 'l', 'x', 'm', 'y', 'b', 't', 'i', 'g', 'p', 'q', 'a'];
    if (Object.keys(raw).some((key) => !allowed.includes(key))) return bad;
    if (typeof raw.l !== 'string' || !/^[01-]{0,64}$/.test(raw.l)) return bad;
    const view = { name: raw.n, layers: {} };
    KEYS.forEach((key, at) => {
      if (raw.l[at] === '1' || raw.l[at] === '0') view.layers[key] = raw.l[at] === '1';
    });
    if (raw.x !== undefined) {
      if (raw.x === null || typeof raw.x !== 'object' || Array.isArray(raw.x)) return bad;
      for (const [key, on] of Object.entries(raw.x)) {
        if ((on !== 0 && on !== 1) || !/^[a-z0-9_-]+\/[a-z0-9_-]+$/.test(key)) return bad;
        view.layers[key] = on === 1;
      }
    }
    if (raw.m !== undefined) {
      if (raw.m === null || typeof raw.m !== 'object' || Array.isArray(raw.m) || Object.keys(raw.m).some((key) => key !== 'o' && key !== 'h')) return bad;
      view.mobs = { only: raw.m.o, hidden: raw.m.h };
    }
    if (raw.y !== undefined) {
      if (raw.y === null || typeof raw.y !== 'object' || Array.isArray(raw.y)) return bad;
      // Every choice a link can carry is in the view, so that one it does
      // not speak of is "all of it" and not whatever the viewer had.
      view.items = Object.fromEntries(SHARED.map((key) => [key, { only: null, hidden: [] }]));
      for (const [at, f] of Object.entries(raw.y)) {
        if (!/^(0|[1-9]\d?)$/.test(at) || Number(at) >= SHARED.length) return bad;
        if (f === null || typeof f !== 'object' || Array.isArray(f) || Object.keys(f).some((key) => !['o', 'h', 's', 'j'].includes(key))) return bad;
        view.items[SHARED[Number(at)]] = { only: f.o, hidden: f.h, ...(f.s !== undefined ? { shown: f.s } : {}), ...(f.j !== undefined ? { mode: 'just', just: f.j } : {}) };
      }
    }
    if (raw.b !== undefined) view.biome = raw.b;
    if (raw.t !== undefined) view.trails = raw.t;
    if (raw.i !== undefined) view.interval = raw.i;
    if (raw.g !== undefined) {
      if (raw.g !== 0 && raw.g !== 1) return bad;
      view.grid = raw.g === 1;
    }
    if (raw.p !== undefined) {
      if (!Array.isArray(raw.p) || raw.p.length !== 4) return bad;
      view.place = { d: raw.p[0], x: raw.p[1], z: raw.p[2], zoom: raw.p[3] };
      if (raw.q !== undefined) {
        if (!Array.isArray(raw.q) || raw.q.length !== 3) return bad;
        view.place.pin = { u: raw.q[0], x: raw.q[1], z: raw.q[2] };
      }
    } else if (raw.q !== undefined) return bad;
    if (raw.a !== undefined) {
      if (raw.a === null || typeof raw.a !== 'object' || Array.isArray(raw.a)) return bad;
      view.look = {};
      for (const [at, value] of Object.entries(raw.a)) {
        if (!/^(0|[1-9]\d?)$/.test(at) || Number(at) >= LOOKS.length) return bad;
        view.look[LOOKS[Number(at)]] = value;
      }
    }
    const read = settings.check.view(view);
    return read ? { view: read } : bad;
  }

  const partOf = (address) => {
    let hash = '';
    try { hash = new URL(address, location.href).hash; } catch { return ''; }
    return hash.slice(1).split('/')[5] || '';
  };

  function addressOf(view) {
    const part = pack(view);
    if (part.length > MAX_LINK) return null;
    const at = view.place ? `${view.place.d}/${view.place.x}/${view.place.z}/${view.place.zoom}` : '-/0/0/0';
    return `${location.origin}${location.pathname}${location.search}#${at}//${part}`;
  }

  async function copyLink(view) {
    const address = addressOf(view);
    if (address === null) {
      say('That view holds too much to fit in a link. Export to a file to pass it on.');
      return;
    }
    try {
      await navigator.clipboard.writeText(address);
      say(`A link to “${view.name}” is copied.${view.players && (view.players.only !== null || view.players.hidden.length > 0) ? ' Which players it hides is not part of a link.' : ''}`);
    } catch {
      say(`Copy it from here: ${address}`);
    }
  }

  // A link is an offer and nothing more: the view is held to one side,
  // and the list is opened on it for the viewer to show, save or dismiss.
  function offerFrom(address) {
    const part = partOf(address);
    if (part === '') return;
    const read = unpack(part);
    if (read.error) {
      pending = `${pending} That link had a view in it that was left out, because ${read.error}.`.trim();
    } else {
      // A second link while the first one's view is being previewed.
      leave(false);
      offered = read.view;
      delete offered.id;
      announced = false;
    }
    arrive();
  }

  // Said, or opened, only once there is a map: not over the login.
  function arrive() {
    if (document.body.classList.contains('locked') || !app.dimension()) return;
    if (pending !== '') {
      tell(pending);
      pending = '';
    }
    if (offered !== null && !announced) {
      announced = true;
      show();
      el.offerShow.focus();
    }
  }

  // --- everything, to a file and back -----------------------------------------

  function exportAll() {
    const doc = { app: 'mcmap', kind: 'settings', v: settings.VERSION, exported: new Date().toISOString(), record: settings.all() };
    const address = URL.createObjectURL(new Blob([JSON.stringify(doc, null, 2)], { type: 'application/json' }));
    const link = document.createElement('a');
    link.href = address;
    link.download = FILE_NAME;
    link.click();
    setTimeout(() => URL.revokeObjectURL(address), 10_000);
    say(`Saved as ${FILE_NAME}: every view and setting, and nothing else.`);
  }

  // What a file holds, read as strictly as a link: { record } or { error }.
  function readFile(text) {
    const bad = { error: 'That file is not one this page exported, or has been changed since: nothing was imported.' };
    let doc;
    try { doc = JSON.parse(text); } catch { return bad; }
    if (doc === null || typeof doc !== 'object' || Array.isArray(doc)) return bad;
    if (Object.keys(doc).some((key) => !['app', 'kind', 'v', 'exported', 'record'].includes(key))) return bad;
    if (doc.app !== 'mcmap' || doc.kind !== 'settings') return bad;
    if (typeof doc.exported !== 'string' || !/^\d{4}-\d\d-\d\dT\d\d:\d\d:\d\d(\.\d{1,3})?Z$/.test(doc.exported)) return bad;
    if (Number.isInteger(doc.v) && doc.v > settings.VERSION) return { error: 'That file is from a later version of this page: nothing was imported.' };
    const record = settings.check.record(doc.record);
    return record ? { record } : bad;
  }

  let incoming = null;

  // The file's settings take the place of the viewer's, and its views are
  // added to theirs: a view that is already in the list, name and all, is
  // not added twice, and none is put in the place of another.
  function merged(record) {
    const mine = settings.get('views');
    const same = (a, b) => JSON.stringify({ ...a, id: '' }) === JSON.stringify({ ...b, id: '' });
    const fresh = record.views.list.filter((view) => !mine.list.some((other) => same(view, other)));
    const taken = new Set(mine.order);
    for (const view of fresh) {
      while (taken.has(view.id)) view.id = newId();
      taken.add(view.id);
    }
    return {
      fresh,
      record: {
        ...record,
        views: { ...mine, list: [...mine.list, ...fresh], order: [...mine.order, ...fresh.map((view) => view.id)], start: mine.start, active: null },
      },
    };
  }

  async function chosen() {
    const [file] = el.file.files;
    el.file.value = '';
    incoming = null;
    el.incoming.hidden = true;
    if (!file) return;
    if (file.size > MAX_FILE) {
      say('That file is larger than anything this page exports: nothing was imported.');
      return;
    }
    let text;
    try { text = await file.text(); } catch {
      say('That file could not be read: nothing was imported.');
      return;
    }
    const read = readFile(text);
    if (read.error) {
      say(read.error);
      return;
    }
    const next = merged(read.record);
    if (next.record.views.list.length > settings.MAX_VIEWS) {
      say(`That file would bring your views to more than the ${settings.MAX_VIEWS} the page keeps: nothing was imported.`);
      return;
    }
    incoming = next;
    el.incomingWhat.textContent = `This file will add ${plural(next.fresh.length, 'view', 'views')} to yours, and put its layers, filters and appearance settings in the place of yours. None of your views is changed.`;
    el.incoming.hidden = false;
    say('');
    el.incomingTake.focus();
  }

  function take() {
    if (!incoming) return;
    const { record, fresh } = incoming;
    incoming = null;
    el.incoming.hidden = true;
    // A file's settings are for keeping, not for a preview to be let go of.
    leave(true);
    if (!settings.replace(record)) {
      say('That file holds more than the page keeps: nothing was imported.');
      return;
    }
    redraw([]);
    say(`Imported: ${plural(fresh.length, 'view', 'views')} added, and the file’s settings are now yours.`);
    refocus = { id: '', act: 'import' };
    render();
  }

  // --- the dialog -------------------------------------------------------------

  function show() {
    expanded = null;
    renaming = null;
    naming = false;
    el.name.value = '';
    if (!undone) say('');
    render();
    if (!el.dialog.open) el.dialog.showModal();
    el.dialog.scrollTop = 0;
  }

  // The button is hidden in the page as it is sent, so that a page whose
  // scripts are from before there were views does not offer a button that
  // does nothing.
  el.open.hidden = false;
  el.open.addEventListener('click', show);
  el.close.addEventListener('click', () => el.dialog.close());
  // A click on the backdrop is a click on the dialog itself, outside
  // everything in it.
  el.dialog.addEventListener('click', (e) => {
    if (e.target === el.dialog) el.dialog.close();
  });
  // The browser gives the focus back to the button that opened the
  // dialog, which on a small screen is in a sheet that has shut by then.
  el.dialog.addEventListener('close', () => {
    incoming = null;
    el.incoming.hidden = true;
    // While a preview is showing, its bar is where the next thing to do is.
    if (before) el.previewBack.focus();
    else if (el.open.offsetParent === null) el.more.focus();
  });
  // A number picks the view with that number, as the list shows them,
  // except while a name is being typed.
  el.dialog.addEventListener('keydown', (e) => {
    if (e.ctrlKey || e.metaKey || e.altKey || e.isComposing || !/^[1-9]$/.test(e.key)) return;
    if (e.target instanceof Element && e.target.matches('input, textarea, select')) return;
    const pick = el.list.querySelectorAll('.view-pick')[Number(e.key) - 1];
    if (!pick) return;
    e.preventDefault();
    pick.click();
  });

  el.form.addEventListener('submit', (e) => {
    e.preventDefault();
    const view = naming && offered ? { ...offered } : capture(el.place.checked, el.look.checked);
    const id = add(view, el.name.value);
    if (id === null) return;
    if (naming) {
      offered = null;
      naming = false;
      // Kept while it was being previewed: the preview ends and the view
      // just saved is shown in its place, this time for keeps.
      if (before) {
        el.name.value = '';
        switchTo(settings.view(id));
        return;
      }
    } else {
      const views = settings.get('views');
      views.active = id;
      settings.set('views', views);
    }
    el.name.value = '';
    refocus = { id, act: 'pick' };
    render();
  });

  el.undo.dataset.act = 'undo';
  el.undo.dataset.id = '';
  el.undo.addEventListener('click', restore);
  el.importAll.dataset.act = 'import';
  el.importAll.dataset.id = '';

  el.offerShow.addEventListener('click', () => {
    if (offered) preview(offered);
  });
  // Naming the offered view, from the offer or from the bar of a preview.
  function saveAs() {
    if (!offered) return;
    if (!el.dialog.open) show();
    naming = true;
    el.name.value = offered.name;
    render();
    el.name.focus();
    el.name.select();
  }
  el.previewKeep.addEventListener('click', saveAs);
  el.previewBack.addEventListener('click', () => leave(false));
  el.offerSave.addEventListener('click', () => {
    saveAs();
  });
  el.offerDrop.addEventListener('click', () => {
    leave(false);
    offered = null;
    naming = false;
    say('The view from the link is dismissed.');
    refocus = { id: '', act: 'none' };
    render();
  });

  el.exportAll.addEventListener('click', exportAll);
  el.importAll.addEventListener('click', () => el.file.click());
  el.file.addEventListener('change', chosen);
  el.incomingTake.addEventListener('click', take);
  el.incomingDrop.addEventListener('click', () => {
    incoming = null;
    el.incoming.hidden = true;
    say('Nothing was imported.');
    el.importAll.focus();
  });

  // The view the page opened with may name layers this server does not
  // offer. That is only known once its layers have had the time to say
  // what they are, so it is said a moment after the map first shows.
  const SETTLE_MS = 4000;
  let opened = settings.get('views').start;
  function settled() {
    const view = opened ? settings.view(opened) : null;
    opened = null;
    if (!view || document.body.classList.contains('locked')) return;
    const have = app.layers.states ? app.layers.states() : {};
    const missing = Object.keys(view.layers).filter((key) => !Object.hasOwn(have, key) && !RETIRED.has(key)).map((key) => LABELS.get(key) || key);
    if (missing.length > 0) tell(`“${view.name}” opened without ${plural(missing.length, 'layer', 'layers')} this map does not have (${missing.slice(0, 4).join(', ')}${missing.length > 4 ? ', …' : ''}).`);
  }
  let waiting = opened !== null;

  document.addEventListener('mcmap:view', () => {
    arrive();
    if (waiting && app.dimension() && !document.body.classList.contains('locked')) {
      waiting = false;
      setTimeout(settled, SETTLE_MS);
      // The chunk it was saved with is pinned as the map first shows, and
      // never over one the viewer has pinned in the meantime.
      // A view that comes with the page has no place, and so no pin.
      const view = settings.view(opened);
      const where = view && view.place ? view.place : null;
      const pin = where ? where.pin : null;
      const at = app.place.get();
      // Nor where an address took the map somewhere else.
      const there = Boolean(where) && Boolean(at) && at.d === where.d && at.x === where.x && at.z === where.z;
      if (pin && there && app.chunk && app.chunk.pin && !app.chunk.pinned()) app.chunk.pin({ unit: pin.u, x: pin.x, z: pin.z });
    }
  });
  addEventListener('hashchange', (e) => offerFrom(e.newURL));
  offerFrom(location.href);

  // For the page's own checks, and for another script that wants to read
  // or write a view the way a link carries one.
  app.views = { capture, apply, pack, unpack, addressOf, readFile, show };
})();
