// Keeps the event dock's rows between renders. A rebuild used to create and
// format all 120 rows on every event, which at fleet event rates was the
// largest single cost on the page; a row now lives as long as its event is
// shown, and an unchanged list touches no DOM at all.

const clock = new Intl.DateTimeFormat([], { hour: '2-digit', minute: '2-digit', second: '2-digit', hour12: false });
export const timeText = at => clock.format(at);

export function createFeedView(box, make) {
  const cache = new Map();
  let shown = '';
  return {
    // rows are events with a unique seq; an empty list shows the placeholder.
    render(rows, placeholder) {
      const key = rows.length ? rows.map(e => e.seq).join(',') : 'empty';
      if (key === shown) return false;
      shown = key;
      if (!rows.length) {
        cache.clear();
        box.replaceChildren(placeholder());
        return true;
      }
      const live = new Set();
      const nodes = rows.map(e => {
        live.add(e.seq);
        let n = cache.get(e.seq);
        if (!n) { n = make(e); cache.set(e.seq, n); }
        return n;
      });
      for (const seq of cache.keys()) if (!live.has(seq)) cache.delete(seq);
      box.replaceChildren(...nodes);
      return true;
    },
  };
}

// Runs fn at most once per frame however often it is asked; a hidden tab
// holds the work until it is shown again, when only the latest state matters.
export function perFrame(fn, raf = requestAnimationFrame) {
  let queued = false;
  return () => {
    if (queued) return;
    queued = true;
    raf(() => { queued = false; fn(); });
  };
}
