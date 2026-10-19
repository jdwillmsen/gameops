'use strict';

// Every player the map has seen, online or not, in a panel beside the map.
// Online players come first; the rest are by when they were last seen. A
// row opens the player's details, which read an offline player from the
// save. The list says where nobody is: a position is the map's to show,
// and the details' for an offline player.
(() => {
  const app = window.mcmap;
  // The page and its scripts are cached apart for a few minutes, so just
  // after a release this can meet a page that has no panel yet.
  if (!app) return;

  const view = {
    open: document.getElementById('roster-open'),
    dialog: document.getElementById('roster'),
    close: document.getElementById('roster-close'),
    filter: document.getElementById('roster-filter'),
    list: document.getElementById('roster-list'),
    said: document.getElementById('roster-said'),
  };
  if (!view.open || !view.dialog || !view.close || !view.filter || !view.list || !view.said || typeof view.dialog.show !== 'function') return;

  // Who is online changes by the second, but the list is for finding a
  // player, not for watching them: the map does that.
  const REFRESH_MS = 30_000;
  // The stylesheet's query for a screen too small for two panels side by
  // side.
  const STACKED = '(max-width: 720px), (max-height: 480px)';
  const TAGS = { online: 'Online', you: 'You', operator: 'Operator' };

  let players = null;
  let ask = null;
  let timer = null;

  const text = (v) => (typeof v === 'string' ? v : '');
  const labelOf = (dimension) => (app.label ? app.label(dimension) : dimension);
  const el = (tag, className, content) => {
    const node = document.createElement(tag);
    if (className) node.className = className;
    if (content !== undefined) node.textContent = content;
    return node;
  };

  function ago(iso) {
    const at = Date.parse(text(iso));
    if (!Number.isFinite(at)) return '';
    const minutes = Math.round((Date.now() - at) / 60_000);
    if (minutes < 2) return 'just now';
    if (minutes < 60) return `${minutes} min ago`;
    const hours = Math.round(minutes / 60);
    if (hours < 48) return `${hours} h ago`;
    return `${Math.round(hours / 24)} days ago`;
  }

  // A player's face, where the map has one for that gamertag.
  function face(name) {
    const heads = app.icons && app.icons.listing ? app.icons.listing().heads : null;
    const key = name.toLowerCase();
    if (!heads || !Object.hasOwn(heads, key) || !heads[key]) return el('span', 'roster-face none');
    const img = el('img', 'roster-face');
    img.alt = '';
    img.width = img.height = 24;
    img.src = `api/icons/head?name=${encodeURIComponent(key)}&v=${encodeURIComponent(heads[key])}`;
    return img;
  }

  // Every name is set as text: a gamertag is a player's choice.
  function row(p) {
    const tags = Array.isArray(p.tags) ? p.tags.filter((t) => Object.hasOwn(TAGS, t)) : [];
    const online = tags.includes('online');
    const b = el('button', 'roster-row');
    b.type = 'button';
    const who = el('span', 'roster-who');
    who.append(el('span', 'roster-name', text(p.name) || 'Player'));
    const earlier = Array.isArray(p.earlier) ? p.earlier.filter((n) => typeof n === 'string' && n !== '') : [];
    const when = online
      ? `Online now${text(p.dimension) ? ` in ${labelOf(p.dimension)}` : ''}`
      : `Last seen ${ago(p.lastSeen) || 'at an unknown time'}`;
    who.append(el('span', 'roster-when', earlier.length > 0 ? `${when} · was ${earlier.join(', ')}` : when));
    const chips = el('span', 'roster-tags');
    for (const t of tags) chips.append(el('span', `roster-tag ${t}`, TAGS[t]));
    b.append(face(text(p.name)), who, chips);
    b.addEventListener('click', () => {
      // Beside each other on a wide screen; on a small one both come up
      // from the bottom, and the list would be under the details.
      if (app.playerDetails && app.playerDetails.open({ id: text(p.id), name: text(p.name) }) && matchMedia(STACKED).matches) shut();
    });
    const li = el('li');
    li.append(b);
    return li;
  }

  const fold = (s) => s.toLocaleLowerCase();

  function draw() {
    if (players === null) return;
    const q = fold(view.filter.value.trim());
    const matching = players.filter((p) => q === ''
      || fold(text(p.name)).includes(q)
      || (Array.isArray(p.earlier) && p.earlier.some((n) => typeof n === 'string' && fold(n).includes(q))));
    view.list.replaceChildren(...matching.map(row));
    const online = players.filter((p) => Array.isArray(p.tags) && p.tags.includes('online')).length;
    if (players.length === 0) note('The map has not seen anyone play yet. Players are listed from the first time they are online.');
    else if (matching.length === 0) note('No player’s gamertag has that in it.');
    else note(`${players.length} ${players.length === 1 ? 'player' : 'players'}, ${online} online.`);
  }

  function note(words) {
    view.said.textContent = words;
  }

  async function fetchIt() {
    if (ask) ask.abort();
    const mine = new AbortController();
    ask = mine;
    try {
      const res = await fetch('api/players', { cache: 'no-store', signal: mine.signal });
      if (ask !== mine) return false;
      if (!res.ok) {
        if (players === null) note(res.status === 404 ? 'This map does not keep a list of players.' : 'The list could not be fetched. Open it again to try once more.');
        return false;
      }
      const body = await res.json();
      if (ask !== mine) return false;
      players = body && Array.isArray(body.players) ? body.players.filter((p) => p && typeof p === 'object' && typeof p.id === 'string') : [];
      draw();
      return true;
    } catch {
      if (ask === mine && players === null) note('The list could not be fetched. Open it again to try once more.');
      return false;
    }
  }

  function show() {
    if (!view.dialog.open) view.dialog.show();
    view.open.setAttribute('aria-expanded', 'true');
    clearInterval(timer);
    timer = setInterval(fetchIt, REFRESH_MS);
    if (players === null) note('Reading who has played…');
    fetchIt();
    view.filter.focus();
  }

  function shut() {
    if (view.dialog.open) view.dialog.close();
  }

  view.open.addEventListener('click', show);
  view.close.addEventListener('click', shut);
  view.filter.addEventListener('input', draw);
  view.dialog.addEventListener('close', () => {
    if (ask) ask.abort();
    ask = null;
    clearInterval(timer);
    view.open.setAttribute('aria-expanded', 'false');
  });
  // A non-modal dialog is not shut by Escape on its own.
  view.dialog.addEventListener('keydown', (e) => {
    if (e.key === 'Escape') {
      e.stopPropagation();
      shut();
    }
  });
  // The faces arrive after the page does.
  document.addEventListener('mcmap:pictures', () => {
    if (view.dialog.open) draw();
  });

  // Offered once the server says it keeps one, which a map without the live
  // layer does not. Asked on the first view of the map that is not behind
  // the login: asked before, it would only be refused.
  let offered = false;
  document.addEventListener('mcmap:view', () => {
    if (offered || document.body.classList.contains('locked')) return;
    offered = true;
    fetchIt().then((ok) => {
      view.open.hidden = !ok;
    });
  });

  app.roster = { show, shut };
})();
