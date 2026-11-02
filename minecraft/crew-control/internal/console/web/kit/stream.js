// One EventSource wrapper for every live feed. The browser retries a dropped
// stream itself, but gives up for good after a non-200 answer (a 401, the
// client cap, a gateway restart); those arrive as readyState CLOSED and are
// rebuilt here after a backoff that doubles from min to max.

// handlers: { eventName: fn(ev) } for named events.
// opts: {
//   min, max       backoff bounds in ms (equal for a fixed delay)
//   onError(es)    runs on every error of the live stream, before any rebuild
//   always         rebuild on any error, not only a closed stream; for feeds
//                  whose address changes as rows arrive, where the browser's
//                  own retry would reuse a stale one
// }
// url may be a function so a rebuild can carry newer state. Returns { close }.
export function openStream(url, handlers, opts = {}) {
  const min = opts.min ?? 2000;
  const max = opts.max ?? 30000;
  let delay = min;
  let source = null;
  let timer = null;

  function connect() {
    timer = null;
    const es = source = new EventSource(typeof url === 'function' ? url() : url);
    for (const name in handlers) {
      es.addEventListener(name, ev => {
        // A delivered event proves the connection works.
        delay = min;
        handlers[name](ev);
      });
    }
    es.onerror = () => {
      // A stream that was replaced or closed on purpose may still report.
      if (source !== es) return;
      if (opts.onError) opts.onError(es);
      if (source !== es) return;
      if (!opts.always && es.readyState !== EventSource.CLOSED) return;
      es.close();
      source = null;
      timer = setTimeout(connect, delay);
      delay = Math.min(delay * 2, max);
    };
  }

  connect();
  return {
    close() {
      clearTimeout(timer);
      timer = null;
      if (source) source.close();
      source = null;
    },
  };
}
