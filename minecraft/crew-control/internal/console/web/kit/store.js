// A small observable record: views read it, mutate it by patch and subscribe
// to the keys they draw. Replaces the host-callback fan-out in console.js.
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
      for (const sub of [...subs]) {
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
