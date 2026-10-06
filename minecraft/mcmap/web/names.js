'use strict';

// What everything on the map is called. The server has the game's own
// names, by the ids the page holds; this fetches that table once, again
// when the server says it has changed, and answers every lookup from it.
// Before the table arrives, and for an id it does not list, the id itself
// is tidied into words, so that nothing on the page is ever shown as an
// identifier or left empty.
(() => {
  const app = window.mcmap;
  if (!app) return;

  // The longest name shown, in characters, which is also the server's.
  const MAX_LENGTH = 64;
  // A table that could not be had is asked for again no sooner than this.
  const RETRY_MS = 5000;
  const GROUPS = ['entities', 'containers', 'beds', 'shulkers', 'structures'];

  const empty = () => Object.fromEntries(GROUPS.map((group) => [group, {}]));
  let table = empty();
  // The version of the table held, or null with none yet.
  let version = null;
  // The version the server last said it has.
  let current = null;
  let askedAt = 0;
  let timer = null;
  let pending = false;
  // A server with no names to give answers 404 until it restarts.
  let gone = false;

  const str = (v) => (typeof v === 'string' ? v : '');
  const digits = (word) => /^\d+$/.test(word);
  const versioned = (word) => /^v\d+$/i.test(word);

  // An id as words: its namespace and any version suffix dropped,
  // underscores to spaces, each word capitalised, exactly as the server
  // tidies one. villager_v2 is Villager. Empty for an id with no letters
  // or digits in it.
  function tidy(id) {
    const from = str(id);
    const words = from.slice(from.lastIndexOf(':') + 1).split(/[^\p{L}\p{Nd}]+/u).filter(Boolean);
    while (words.length > 1 && (versioned(words.at(-1)) || (digits(words.at(-1)) && words.length > 2 && versioned(words.at(-2))))) {
      words.pop();
    }
    const out = [];
    let length = 0;
    for (const word of words) {
      const letters = [...word.toLowerCase()];
      if (length + letters.length + 1 > MAX_LENGTH) break;
      length += letters.length + (out.length > 0 ? 1 : 0);
      out.push(letters[0].toUpperCase() + letters.slice(1).join(''));
    }
    return out.join(' ');
  }

  const held = (group, id) => {
    const names = table[group];
    return typeof id === 'string' && Object.hasOwn(names, id) ? str(names[id]) : '';
  };

  const entity = (id) => held('entities', str(id).replace(/^minecraft:/, '')) || tidy(id) || 'Mob';

  function container(kind, { trapped = false } = {}) {
    const key = trapped && kind === 'chest' ? 'trapped_chest' : kind;
    return held('containers', key) || (key === 'shulker' ? 'Shulker Box' : tidy(key)) || 'Container';
  }

  const bed = (colour) => held('beds', colour) || (tidy(colour) ? `${tidy(colour)} Bed` : held('beds', 'default') || 'Bed');

  function shulker(colour) {
    const plain = !tidy(colour) || colour === 'undyed';
    return held('shulkers', colour) || (plain ? held('shulkers', 'default') || container('shulker') : `${tidy(colour)} Shulker Box`);
  }

  const structure = (kind) => held('structures', kind) || tidy(kind) || 'Structure';

  // A container marker as the server sends one: its kind, and where the
  // world says, its colour or that it is trapped.
  const holder = (kind, colour, trapped) => (kind === 'shulker' ? shulker(colour) : container(kind, { trapped: trapped === true }));

  // A mob's type with what else is known of it, and the whole of a mob:
  // its name tag, where it has one, with the type after.
  const kindOf = (type, baby) => (baby === true ? `${entity(type)}, baby` : entity(type));
  const mob = (name, type, baby) => (str(name) ? `${str(name)} (${kindOf(type, baby)})` : kindOf(type, baby));

  // More than one of a thing, for a row that counts them. Only ever given
  // a name that ends in an ordinary English noun.
  function plural(name) {
    if (/[^aeiou]y$/i.test(name)) return `${name.slice(0, -1)}ies`;
    return /(s|x|z|ch|sh)$/i.test(name) ? `${name}es` : `${name}s`;
  }

  function take(doc) {
    const next = empty();
    for (const group of GROUPS) {
      const from = doc && doc[group];
      if (!from || typeof from !== 'object' || Array.isArray(from)) continue;
      for (const [id, name] of Object.entries(from)) {
        // Whatever the answer holds, a name is short text and nothing else.
        if (typeof name === 'string' && name.trim() !== '') next[group][id] = name.slice(0, MAX_LENGTH);
      }
    }
    table = next;
    version = str(doc && doc.version);
    // Everything drawn with a name is drawn again by whoever drew it.
    document.dispatchEvent(new CustomEvent('mcmap:names'));
  }

  async function load() {
    if (pending || gone) return;
    const wait = askedAt + RETRY_MS - Date.now();
    if (wait > 0) {
      clearTimeout(timer);
      timer = setTimeout(sync, wait);
      return;
    }
    pending = true;
    askedAt = Date.now();
    const asked = current;
    try {
      // The browser keeps the last answer and asks whether it still
      // stands, so an unchanged table costs a 304.
      const res = await fetch('api/names', { cache: 'no-cache' });
      if (res.status === 404) {
        gone = true;
        return;
      }
      if (!res.ok) return;
      const doc = await res.json();
      if (str(doc && doc.version) !== version) take(doc);
    } catch {
      /* the tidied ids stand until the next try */
    } finally {
      pending = false;
      // Another version was announced while this one was on its way.
      if (current !== asked) sync();
    }
  }

  // Asks for the table if there is none, or the server has another.
  function sync() {
    if (document.body.classList.contains('locked') || !app.dimension() || document.hidden) return;
    if (version === null || (current !== null && current !== version)) load();
  }

  app.names = { entity, container, bed, shulker, structure, holder, kindOf, mob, plural, tidy: (id) => tidy(id) || 'Unknown' };

  // The list of pictures says which version of the names there is, so the
  // table is asked for again only when that has changed.
  document.addEventListener('mcmap:icons', (e) => {
    const listed = e.detail && e.detail.names;
    if (typeof listed !== 'string' || listed === '') return;
    current = listed;
    sync();
  });
  document.addEventListener('mcmap:view', sync);
  document.addEventListener('visibilitychange', sync);
})();
