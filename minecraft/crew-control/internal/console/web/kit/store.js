// A small observable record: views read it, change it by patch and subscribe
// to the keys they draw, so a feed that changes one key redraws only the
// views that show it.
export function createStore(initial) {
  let state = { ...initial };
  const subs = new Set();
  return {
    get: () => state,
    set(patch) {
      const prev = state;
      const changed = Object.keys(patch).filter(k => !Object.is(prev[k], patch[k]));
      if (!changed.length) return;
      state = { ...prev, ...patch };
      // A copy, so a subscriber that unsubscribes mid-notify does not skip its neighbour.
      for (const sub of [...subs]) {
        if (!subs.has(sub)) continue;
        if (!sub.keys.length || sub.keys.some(k => changed.includes(k))) sub.fn(state, prev);
      }
    },
    // With keys, fn runs only when one of them changed; returns unsubscribe.
    subscribe(fn, ...keys) {
      const sub = { fn, keys };
      subs.add(sub);
      return () => subs.delete(sub);
    },
  };
}
