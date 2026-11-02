// One place for keyboard shortcuts: a typing guard and layered Escape, so a
// view no longer chains its own Escape branch by hand into the global handler.

// The console's shortcuts are plain letters and digits, so they must not fire
// while an operator types.
export function isTyping(t) {
  return !!t && (['INPUT', 'SELECT', 'TEXTAREA'].includes(t.tagName) || t.isContentEditable === true);
}

export function createKeys() {
  const layers = [];
  const binds = new Map();

  return {
    // Escape layers answer newest first; one returns true when it used the key.
    // The newest layer is the one on top (an overlay over a view).
    escape(fn) {
      layers.push(fn);
      return () => {
        const at = layers.lastIndexOf(fn);
        if (at >= 0) layers.splice(at, 1);
      };
    },

    // bind('j', fn): fn(ev) returns true when it used the key. Plain keys only,
    // and never while typing, unless opts.typing / opts.mod say otherwise.
    bind(key, fn, opts = {}) {
      const entry = { fn, typing: !!opts.typing, mod: !!opts.mod };
      const k = opts.mod ? key.toLowerCase() : key;
      if (!binds.has(k)) binds.set(k, []);
      binds.get(k).push(entry);
      return () => {
        const list = binds.get(k);
        const at = list.indexOf(entry);
        if (at >= 0) list.splice(at, 1);
      };
    },

    // True when a layer or binding used the key.
    handle(ev) {
      const mod = !!(ev.ctrlKey || ev.metaKey);
      if (ev.altKey) return false;
      const typing = isTyping(ev.target);
      if (ev.key === 'Escape' && !mod) {
        if (typing) return false;
        for (let i = layers.length - 1; i >= 0; i--) if (layers[i](ev)) return true;
        return false;
      }
      const list = binds.get(mod ? String(ev.key).toLowerCase() : ev.key) || [];
      for (let i = list.length - 1; i >= 0; i--) {
        const e = list[i];
        if (e.mod !== mod || (typing && !e.typing)) continue;
        if (e.fn(ev)) return true;
      }
      return false;
    },

    attach(target) {
      const on = ev => { this.handle(ev); };
      target.addEventListener('keydown', on);
      return () => target.removeEventListener('keydown', on);
    },
  };
}
