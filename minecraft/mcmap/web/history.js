'use strict';

// The map as it was. The server keeps the terrain of one cycle a day, and
// this is the control that goes back through those days: it tells the map
// which day to draw and the map draws that day's tiles where it draws
// today's. Only the terrain is of the day; everything laid over it is
// still the world as it is now, and the control says so.
(() => {
  const app = window.mcmap;
  // The page and its scripts are cached apart for a few minutes, so just
  // after a release this can meet a map that cannot yet be told a day.
  if (!app || !app.past) return;
  // Inside the map's own box, so it is over the map wherever the panel
  // and the bars around the map happen to be.
  const stage = document.getElementById('map');
  if (!stage) return;

  const STEP_MS = 1500;
  // A day is added once a day, so the list is asked for again only now
  // and then; a server that could not be reached is tried sooner.
  const ASK_AGAIN_MS = 10 * 60_000;
  const RETRY_MS = 60_000;
  const DAY = /^\d{4}-\d{2}-\d{2}$/;

  const make = (tag, part, text) => {
    const node = document.createElement(tag);
    node.className = `timeline-${part}`;
    if (text) node.textContent = text;
    return node;
  };
  const button = (part, text, label) => {
    const node = make('button', part, text);
    node.type = 'button';
    if (label) node.setAttribute('aria-label', label);
    return node;
  };

  const bar = document.createElement('section');
  bar.className = 'timeline';
  bar.setAttribute('aria-label', 'Map history');
  bar.hidden = true;
  const open = button('open', 'History');
  open.setAttribute('aria-expanded', 'false');
  const body = make('div', 'body');
  body.hidden = true;
  const earlier = button('earlier', '‹', 'The day before');
  const later = button('later', '›', 'The day after');
  const range = make('input', 'range');
  range.type = 'range';
  range.min = '0';
  range.step = '1';
  range.setAttribute('aria-label', 'Day shown');
  const shown = make('output', 'day');
  const play = button('play', 'Play');
  const now = button('now', 'Now');
  const note = make('p', 'note');
  note.setAttribute('role', 'status');
  body.append(earlier, range, later, shown, play, now, note);
  bar.append(open, body);
  stage.append(bar);
  // Being in the map's box, a press or a turn of the wheel on it would
  // otherwise be the map's too: dragging the day along would drag the map.
  if (window.L && L.DomEvent) {
    L.DomEvent.disableClickPropagation(bar);
    L.DomEvent.disableScrollPropagation(bar);
  }

  // The days kept, oldest first. at is the one shown; one past the last
  // is now, which is where the control rests.
  let days = [];
  let at = 0;
  let gone = false;
  let askAfter = 0;
  let timer = null;

  // A day is a date in UTC, and is said as that date wherever it is read.
  const name = (day) => new Date(`${day}T00:00:00Z`).toLocaleDateString(undefined, {
    weekday: 'short', day: 'numeric', month: 'short', year: 'numeric', timeZone: 'UTC',
  });

  function draw() {
    const past = at < days.length;
    range.max = String(days.length);
    range.value = String(at);
    earlier.disabled = at <= 0;
    later.disabled = !past;
    now.disabled = !past;
    play.textContent = timer === null ? 'Play' : 'Pause';
    shown.textContent = past ? name(days[at].day) : 'Now';
    range.setAttribute('aria-valuetext', shown.textContent);
    if (!past) {
      note.textContent = days.length === 0 ? '' : `${days.length} ${days.length === 1 ? 'day' : 'days'} kept, from ${name(days[0].day)}.`;
    } else if (app.dimension() && !days[at].dimensions.some((d) => d && d.id === app.dimension())) {
      note.textContent = 'This day kept nothing of this dimension, so no terrain is drawn. Players, markers, structures and biomes are as they are now.';
    } else {
      note.textContent = `The terrain is of this day. Players, markers, structures and biomes are as they are now.${days[at].rendererChanged ? ' It was drawn by a different renderer than the day before, so not every difference from it is the world\'s.' : ''}`;
    }
    document.body.classList.toggle('past', past);
  }

  function choose(to) {
    const next = Math.max(0, Math.min(days.length, Math.round(to)));
    if (!Number.isFinite(next) || next === at) return;
    at = next;
    app.past.set(at < days.length ? days[at] : null);
    draw();
  }

  function stop() {
    if (timer !== null) clearInterval(timer);
    timer = null;
  }

  function beat() {
    choose(at + 1);
    if (at >= days.length) {
      stop();
      draw();
    }
  }

  async function ask() {
    if (gone || document.body.classList.contains('locked') || Date.now() < askAfter) return;
    askAfter = Date.now() + ASK_AGAIN_MS;
    let listed;
    try {
      const res = await fetch('api/history', { cache: 'no-store' });
      if (res.status === 404) {
        // A server that keeps no history says so once.
        gone = true;
        return;
      }
      if (!res.ok) throw new Error(String(res.status));
      listed = (await res.json()).days;
    } catch {
      askAfter = Date.now() + RETRY_MS;
      return;
    }
    const kept = (Array.isArray(listed) ? listed : []).filter((d) => d && DAY.test(d.day) && Array.isArray(d.dimensions));
    // The day shown stays the day shown while days are added after it.
    const was = at < days.length ? days[at].day : '';
    days = kept;
    at = was ? days.findIndex((d) => d.day === was) : days.length;
    if (at < 0) {
      at = days.length;
      app.past.set(null);
    }
    bar.hidden = days.length === 0;
    draw();
  }

  open.addEventListener('click', () => {
    const opening = body.hidden;
    body.hidden = !opening;
    open.setAttribute('aria-expanded', String(opening));
    if (!opening) {
      // Put away, the control must not leave the map on a day with
      // nothing in sight saying so.
      stop();
      choose(days.length);
      draw();
    }
  });
  earlier.addEventListener('click', () => { stop(); choose(at - 1); draw(); });
  later.addEventListener('click', () => { stop(); choose(at + 1); draw(); });
  now.addEventListener('click', () => { stop(); choose(days.length); draw(); });
  range.addEventListener('input', () => { stop(); choose(Number(range.value)); draw(); });
  play.addEventListener('click', () => {
    if (timer !== null) {
      stop();
      draw();
      return;
    }
    if (days.length === 0) return;
    // From now there is nowhere later to run to, so it starts over.
    if (at >= days.length) choose(0);
    timer = setInterval(beat, STEP_MS);
    draw();
  });

  // What a day's note says depends on the dimension shown, which may be
  // what has changed.
  document.addEventListener('mcmap:view', () => {
    draw();
    ask();
  });
  ask();
})();
