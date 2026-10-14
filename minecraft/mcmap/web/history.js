'use strict';

// The map as it was. The server keeps the terrain of one cycle a day, and
// this is the control that goes back through those days: it tells the map
// which day to draw and the map draws that day's tiles where it draws
// today's. The terrain is of the day, and so are the beds, containers and
// named mobs where the day kept them; everything else laid over it is
// still the world as it is now, and the control says which is which.
//
// It is a button in the corner of the map that opens a dock under it: the
// day shown, which opens a calendar of the days kept, what is of that day
// and what of now, the steps and the play through the days, and a scrubber
// with a tick for every day since the first one kept.
(() => {
  const app = window.mcmap;
  // The page and its scripts are cached apart for a few minutes, so just
  // after a release this can meet a map that cannot yet be told a day.
  if (!app || !app.past) return;
  const stage = document.getElementById('map');
  if (!stage || !stage.parentNode) return;
  const settings = window.mcmapSettings || null;

  const STEP_MS = 1500;
  // How many days a play shows in the time it would show one.
  const SPEEDS = [1, 2, 4];
  const PACES = { 1: 'the usual speed', 2: 'twice as fast', 4: 'four times as fast' };
  // A day is added once a day, so the list is asked for again only now
  // and then; a server that could not be reached is tried sooner.
  const ASK_AGAIN_MS = 10 * 60_000;
  const RETRY_MS = 60_000;
  const DAY = /^\d{4}-\d{2}-\d{2}$/;
  const DAY_MS = 86_400_000;
  // How many days kept the keys that go further than one step go.
  const LEAP = 7;
  const WEEK = ['Mo', 'Tu', 'We', 'Th', 'Fr', 'Sa', 'Su'];

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

  // The dock is under the map and not over it. The map's box is made the
  // top of a column of its own, in the place it had beside the layer panel,
  // and the dock is the column's foot: opened, it takes its height from
  // the map alone, which hears of its new size by itself.
  const hall = stage.parentNode;
  const column = document.createElement('div');
  column.className = 'timeline-column';
  hall.insertBefore(column, stage);
  column.append(stage);

  const bar = document.createElement('section');
  bar.className = 'timeline';
  bar.setAttribute('aria-label', 'Map history');
  bar.hidden = true;
  const open = button('open', 'History');
  open.setAttribute('aria-expanded', 'false');
  const body = make('div', 'body');
  body.hidden = true;

  const date = button('date');
  date.setAttribute('aria-haspopup', 'dialog');
  date.setAttribute('aria-expanded', 'false');
  date.title = 'Pick a day from a calendar';
  const shown = make('output', 'day');
  date.append(shown);
  const note = make('p', 'note');
  note.setAttribute('role', 'status');
  // On a small screen the note is cut to two lines until asked for whole.
  const more = button('more', 'More');
  more.classList.add('link');
  more.setAttribute('aria-expanded', 'false');

  const controls = make('div', 'controls');
  const earlier = button('earlier', '‹', 'The day before');
  const play = button('play', 'Play');
  const later = button('later', '›', 'The day after');
  const speeds = make('div', 'speed');
  speeds.classList.add('segmented');
  speeds.setAttribute('role', 'radiogroup');
  speeds.setAttribute('aria-label', 'Speed of a play');
  const paces = SPEEDS.map((times) => {
    const choice = button('times', `${times}×`, `Play at ${PACES[times]}`);
    choice.classList.add('segment');
    choice.setAttribute('role', 'radio');
    return choice;
  });
  speeds.append(...paces);
  // Where there is no room for the three, one button that goes round them.
  const pace = button('pace');
  const loop = button('loop', 'Loop');
  loop.title = 'Start again from the first day in place of stopping';
  loop.setAttribute('aria-pressed', 'false');
  const now = button('now', 'Now');
  const close = button('close', '', 'Close the history');
  close.classList.add('clear');
  controls.append(earlier, play, later, speeds, pace, loop, now, close);

  // The scrubber: a slider whose steps are the days kept and now, drawn as
  // every calendar day from the first one kept, so that the days nothing
  // was kept of are seen as gaps and a month is as wide as it was long.
  const scrub = make('div', 'scrub');
  const range = make('div', 'range');
  range.setAttribute('role', 'slider');
  range.setAttribute('aria-label', 'Day shown');
  range.setAttribute('aria-valuemin', '0');
  range.tabIndex = 0;
  const months = make('div', 'months');
  months.setAttribute('aria-hidden', 'true');
  const ticks = make('div', 'ticks');
  ticks.setAttribute('aria-hidden', 'true');
  const handle = make('span', 'handle');
  range.append(months, ticks, handle);
  scrub.append(range);

  // The calendar hangs from the dock over the map's corner. It is not a
  // dialog of the browser's, which would take the map away while it is
  // open: the map under it is still the map.
  const calendar = make('div', 'calendar');
  calendar.setAttribute('role', 'dialog');
  calendar.setAttribute('aria-modal', 'false');
  calendar.setAttribute('aria-label', 'Pick a day');
  calendar.hidden = true;
  const head = make('div', 'calendar-head');
  const before = button('month-before', '‹', 'The month before');
  const title = make('strong', 'month');
  const after = button('month-after', '›', 'The month after');
  head.append(before, title, after);
  const grid = make('div', 'grid');
  const foot = make('p', 'kept');
  const legend = make('span', 'legend');
  legend.append(make('i', 'key'), 'No day kept');
  const count = make('span', 'count');
  foot.append(legend, count);
  calendar.append(head, grid, foot);

  body.append(date, controls, note, more, scrub, calendar);
  bar.append(open, body);
  column.append(bar);

  // The days kept, oldest first. at is the one shown; one past the last
  // is now, which is where the control rests.
  let days = [];
  let at = 0;
  let gone = false;
  let askAfter = 0;
  let timer = null;
  let speed = 1;
  let looped = false;

  // A day is a date in UTC, and is said as that date wherever it is read.
  const utc = (day) => Date.parse(`${day}T00:00:00Z`);
  const dayAt = (ms) => new Date(ms).toISOString().slice(0, 10);
  const written = (day, how) => new Date(utc(day)).toLocaleDateString(undefined, { ...how, timeZone: 'UTC' });
  const name = (day) => written(day, { weekday: 'short', day: 'numeric', month: 'short', year: 'numeric' });

  // Everything the map draws that is never of a day. The second is said by
  // a page whose markers do not follow the day: its map or its markers'
  // script is from before they could, and draws today's.
  const NOW = 'Players, loaded mobs, waypoints, structures and biomes are as they are now.';
  const NOW_AND_MARKERS = 'Players, markers, structures and biomes are as they are now.';
  const followed = () => Boolean(app.past.markers && app.markers && app.markers.day);

  // What a day's note says is of the day and what is of now, for the
  // dimension shown.
  function said(day) {
    const kept = day.dimensions.find((d) => d && d.id === app.dimension());
    if (app.dimension() && !kept) {
      return followed()
        ? `This day kept nothing of this dimension, so no terrain is drawn, and no beds, containers or named mobs. ${NOW}`
        : `This day kept nothing of this dimension, so no terrain is drawn. ${NOW_AND_MARKERS}`;
    }
    let text;
    if (!followed()) text = `The terrain is of this day. ${NOW_AND_MARKERS}`;
    else if (kept && kept.markers === true) text = `The terrain is of this day, and so are the beds, containers and named mobs, each where it was saved then. ${NOW}`;
    else text = `The terrain is of this day. Its beds, containers and named mobs were not kept, so none are drawn. ${NOW}`;
    return day.rendererChanged ? `${text} It was drawn by a different renderer than the day before, so not every difference from it is the world's.` : text;
  }

  // Every calendar day from the first one kept to today, each with its
  // place among the days kept or -1 for a day nothing was kept of, and
  // for each day kept its place among those. The scrubber has a slot for
  // each of them and one more, at the end, for now.
  let span = [];
  let where = [];
  let laid = '';

  function lay() {
    const today = dayAt(Date.now());
    const sign = `${today} ${days.map((d) => d.day).join(' ')}`;
    if (sign === laid) return;
    laid = sign;
    span = [];
    where = [];
    if (days.length > 0) {
      const held = new Map(days.map((d, i) => [d.day, i]));
      // A day kept after today, by a clock that is ahead of this one, is
      // still a day to go to.
      const end = Math.max(utc(today), utc(days[days.length - 1].day));
      for (let t = utc(days[0].day); t <= end; t += DAY_MS) {
        const day = dayAt(t);
        const index = held.has(day) ? held.get(day) : -1;
        if (index >= 0) where[index] = span.length;
        span.push({ day, kept: index });
      }
    }
    const slots = span.length + 1;
    range.style.setProperty('--timeline-slots', String(slots));
    const marks = [];
    const labels = [];
    let dated = false;
    span.forEach((d, i) => {
      const tick = make('i', 'tick');
      tick.classList.add(d.kept >= 0 ? 'kept' : 'gap');
      const starts = d.day.endsWith('-01');
      if (starts) tick.classList.add('month');
      marks.push(tick);
      // The first day's month is named where the days begin, unless the
      // next month begins before there is room for a name.
      if (!starts && (i !== 0 || span.slice(1, 4).some((o) => o.day.endsWith('-01')))) return;
      // The year is said once, and again with each January: by the first
      // name unless the next is a fortnight or less after it, which on a
      // narrow scrubber leaves room for a month and not for its year.
      const cramped = span.slice(i + 1, i + 15).some((o) => o.day.endsWith('-01'));
      const year = (!dated && !cramped) || d.day.slice(5, 7) === '01';
      if (year) dated = true;
      const label = make('span', 'month-name', written(d.day, year ? { month: 'short', year: 'numeric' } : { month: 'short' }));
      label.style.setProperty('left', `${(i / slots) * 100}%`);
      labels.push(label);
    });
    const last = make('i', 'tick');
    last.classList.add('now');
    const end = make('span', 'month-name', 'Now');
    end.classList.add('now');
    ticks.replaceChildren(...marks, last);
    months.replaceChildren(...labels, end);
  }

  // The scrubber scrolls where its days are more than fit, and the day
  // shown is brought into sight when it changes, with room beside it to
  // go on from. Not otherwise: a viewer who has scrolled away to look is
  // left looking.
  let seen = -1;
  function reveal(slot) {
    if (slot === seen) return;
    seen = slot;
    const wide = scrub.clientWidth;
    const all = scrub.scrollWidth;
    if (!(wide > 0) || !(all > wide)) return;
    const x = ((slot + 0.5) / (span.length + 1)) * all;
    const room = Math.min(48, wide / 4);
    if (x < scrub.scrollLeft + room) scrub.scrollLeft = Math.max(0, x - room);
    else if (x > scrub.scrollLeft + wide - room) scrub.scrollLeft = x - wide + room;
  }

  function draw() {
    lay();
    const past = at < days.length;
    const slot = past ? where[at] : span.length;
    range.setAttribute('aria-valuemax', String(days.length));
    range.setAttribute('aria-valuenow', String(at));
    range.style.setProperty('--timeline-at', String(slot));
    earlier.disabled = at <= 0;
    later.disabled = !past;
    now.disabled = !past;
    play.textContent = timer === null ? 'Play' : 'Pause';
    shown.textContent = past ? name(days[at].day) : 'Now';
    date.setAttribute('aria-label', `${shown.textContent}. Pick a day from a calendar`);
    range.setAttribute('aria-valuetext', shown.textContent);
    paces.forEach((choice, i) => {
      choice.setAttribute('aria-checked', String(SPEEDS[i] === speed));
      choice.tabIndex = SPEEDS[i] === speed ? 0 : -1;
    });
    pace.textContent = `${speed}×`;
    pace.setAttribute('aria-label', `Playing at ${PACES[speed]}. Press to change`);
    loop.setAttribute('aria-pressed', String(looped));
    if (!past) {
      note.textContent = days.length === 0 ? '' : `${days.length} ${days.length === 1 ? 'day' : 'days'} kept, from ${name(days[0].day)}.`;
    } else {
      note.textContent = said(days[at]);
    }
    document.body.classList.toggle('past', past);
    if (!calendar.hidden) fill();
    reveal(slot);
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

  // What lies over the foot of the map's column is told how much of it
  // the dock has taken, to keep clear of it.
  function room() {
    hall.style.setProperty('--timeline-room', `${body.hidden ? 0 : body.offsetHeight}px`);
  }
  if (typeof ResizeObserver === 'function') new ResizeObserver(room).observe(body);

  function unfold(opening) {
    if (!opening) pick(false);
    body.hidden = !opening;
    // The dock has a button of its own to put it away.
    open.hidden = opening;
    open.setAttribute('aria-expanded', String(opening));
    document.body.classList.toggle('timeline-shown', opening);
    seen = -1;
    room();
  }

  // Put away, the control must not leave the map on a day with nothing in
  // sight saying so.
  function shut() {
    stop();
    unfold(false);
    choose(days.length);
    draw();
  }

  // The address can name a day: the one a link was copied on. Only the
  // map reads the address, and only here is it known which days are kept,
  // so the map says which day is wanted and is told that day, or now when
  // it is not one of them, which also takes it out of the address. Only
  // once the days are known: before that every day would look not kept.
  // A map from before it could say has nothing to follow.
  let known = false;
  function follow() {
    if (!known || !app.past.wanted) return;
    const named = app.past.wanted();
    if (named === (at < days.length ? days[at].day : '')) return;
    const to = named ? days.findIndex((d) => d.day === named) : days.length;
    stop();
    at = to < 0 ? days.length : to;
    app.past.set(at < days.length ? days[at] : null);
    // A day is never shown with the control that says so put away.
    if (at < days.length) unfold(true);
    draw();
  }

  // A play that is not to start over rests on now.
  function beat() {
    if (looped && at + 1 >= days.length) choose(0);
    else choose(at + 1);
    if (!looped && at >= days.length) stop();
    draw();
  }

  function run() {
    stop();
    timer = setInterval(beat, STEP_MS / speed);
  }

  // How fast a play goes and whether it starts over are kept with the
  // page's other settings, where they have a place: the settings' script
  // may be one from before they had, and they then last as long as the
  // page does.
  const stored = () => (settings ? settings.get('history') : null);
  function recall() {
    const was = stored();
    if (!was) return;
    if (SPEEDS.includes(was.speed)) speed = was.speed;
    looped = was.loop === true;
  }
  function keep() {
    if (stored()) settings.set('history', { speed, loop: looped });
  }

  function hurry(to) {
    if (!SPEEDS.includes(to) || to === speed) return;
    speed = to;
    keep();
    if (timer !== null) run();
    draw();
  }

  async function ask() {
    // Not before the map has shown a dimension, which is the first sign
    // that there is a session: the page is not locked until the map has
    // refused it, and asking ahead of that is a request only ever refused.
    if (gone || !app.dimension() || document.body.classList.contains('locked') || Date.now() < askAfter) return;
    askAfter = Date.now() + ASK_AGAIN_MS;
    let listed;
    try {
      const res = await fetch('api/history', { cache: 'no-store' });
      if (res.status === 404) {
        // A server that keeps no history says so once, and has no day
        // an address could name.
        gone = true;
        known = true;
        follow();
        return;
      }
      if (!res.ok) throw new Error(String(res.status));
      listed = (await res.json()).days;
    } catch {
      askAfter = Date.now() + RETRY_MS;
      return;
    }
    // A date there is no such day as has no place among the others.
    const kept = (Array.isArray(listed) ? listed : []).filter((d) => d && DAY.test(d.day) && Array.isArray(d.dimensions) && Number.isFinite(utc(d.day)) && dayAt(utc(d.day)) === d.day);
    // The day shown stays the day shown while days are added after it.
    const was = at < days.length ? days[at].day : '';
    days = kept;
    at = was ? days.findIndex((d) => d.day === was) : days.length;
    if (at < 0) {
      at = days.length;
      app.past.set(null);
    }
    bar.hidden = days.length === 0;
    if (days.length === 0) unfold(false);
    known = true;
    draw();
    follow();
  }

  // --- the calendar -----------------------------------------------------------
  //
  // A month of the days kept, to go to one by its date. A day kept is a
  // button; a day nothing was kept of is struck through and cannot be
  // pressed, and neither can a day before the first one kept or after
  // today. The months it turns through are those that have a day.

  let month = '';
  let filled = '';
  let cells = new Map();

  const monthOf = (year, index) => new Date(Date.UTC(year, index, 1)).toISOString().slice(0, 7);

  function fill() {
    const chosen = at < days.length ? days[at].day : '';
    const first = days[0].day.slice(0, 7);
    const last = days[days.length - 1].day.slice(0, 7);
    if (!month) month = (chosen || days[days.length - 1].day).slice(0, 7);
    if (month < first) month = first;
    if (month > last) month = last;
    before.disabled = month <= first;
    after.disabled = month >= last;
    const sign = `${month} ${chosen} ${laid}`;
    if (sign === filled) return;
    filled = sign;

    const [year, m] = month.split('-').map(Number);
    title.textContent = written(`${month}-01`, { month: 'long', year: 'numeric' });
    const held = new Map(days.map((d, i) => [d.day, i]));
    const from = span[0].day;
    const to = span[span.length - 1].day;
    const focused = grid.contains(document.activeElement) ? document.activeElement.dataset.day : '';
    const nodes = WEEK.map((d) => make('span', 'weekday', d));
    // Weeks begin on Monday.
    const lead = (new Date(Date.UTC(year, m - 1, 1)).getUTCDay() + 6) % 7;
    for (let i = 0; i < lead; i += 1) nodes.push(make('span', 'blank'));
    const length = new Date(Date.UTC(year, m, 0)).getUTCDate();
    cells = new Map();
    let kept = 0;
    let within = 0;
    for (let d = 1; d <= length; d += 1) {
      const day = `${month}-${String(d).padStart(2, '0')}`;
      const cell = button('cell', String(d));
      cell.dataset.day = day;
      const inside = day >= from && day <= to;
      if (inside) within += 1;
      if (held.has(day)) {
        kept += 1;
        cell.setAttribute('aria-label', name(day));
        cell.setAttribute('aria-pressed', String(day === chosen));
      } else {
        cell.disabled = true;
        if (inside) cell.classList.add('gap');
        cell.setAttribute('aria-label', `${name(day)}, ${inside ? 'no day kept' : 'outside the days kept'}`);
      }
      cells.set(day, cell);
      nodes.push(cell);
    }
    grid.replaceChildren(...nodes);
    // Of the month's days that could have been kept, not of all its days:
    // the month the first day is in, and this month, are not yet whole.
    count.textContent = `${kept} of ${within} ${within === 1 ? 'day' : 'days'} kept`;
    const back = cells.get(focused);
    if (back && !back.disabled) back.focus();
  }

  function pick(opening) {
    if (calendar.hidden === !opening) return;
    calendar.hidden = !opening;
    date.setAttribute('aria-expanded', String(opening));
    if (!opening) return;
    month = '';
    filled = '';
    fill();
    const chosen = at < days.length ? cells.get(days[at].day) : null;
    const first = chosen || [...cells.values()].find((cell) => !cell.disabled);
    if (first) first.focus();
  }

  function turn(by) {
    const [year, m] = month.split('-').map(Number);
    month = monthOf(year, m - 1 + by);
    fill();
  }

  date.addEventListener('click', () => pick(calendar.hidden));
  before.addEventListener('click', () => turn(-1));
  after.addEventListener('click', () => turn(1));
  grid.addEventListener('click', (e) => {
    const cell = e.target instanceof Element ? e.target.closest('.timeline-cell') : null;
    if (!cell || cell.disabled) return;
    const to = days.findIndex((d) => d.day === cell.dataset.day);
    if (to < 0) return;
    stop();
    pick(false);
    choose(to);
    draw();
    date.focus();
  });
  // The arrow keys go to the next day kept that way, a day or a week on.
  grid.addEventListener('keydown', (e) => {
    const by = { ArrowLeft: -1, ArrowRight: 1, ArrowUp: -7, ArrowDown: 7 }[e.key];
    const from = e.target instanceof Element && e.target.dataset ? e.target.dataset.day : '';
    if (!by || !from) return;
    e.preventDefault();
    for (let t = utc(from) + by * DAY_MS; cells.has(dayAt(t)); t += by * DAY_MS) {
      const cell = cells.get(dayAt(t));
      if (cell.disabled) continue;
      cell.focus();
      return;
    }
  });
  // Escape puts the calendar away and gives the focus back to the day,
  // heard on the way down so that it is not also an Escape for the card;
  // a press anywhere else puts it away and leaves the focus where it went.
  document.addEventListener('keydown', (e) => {
    if (e.key !== 'Escape' || calendar.hidden) return;
    e.stopPropagation();
    pick(false);
    date.focus();
  }, true);
  document.addEventListener('pointerdown', (e) => {
    if (calendar.hidden || !(e.target instanceof Node)) return;
    if (!calendar.contains(e.target) && !date.contains(e.target)) pick(false);
  });

  // --- the scrubber -------------------------------------------------------------
  //
  // A press or a drag along it goes to the day kept nearest the pointer,
  // or to now at its end: a day nothing was kept of is passed over, to
  // the nearer of the days either side.

  function nearest(clientX) {
    const box = range.getBoundingClientRect();
    if (!(box.width > 0)) return null;
    const slot = Math.max(0, Math.min(span.length, Math.floor(((clientX - box.left) / box.width) * (span.length + 1))));
    let best = days.length;
    // Of a day and now as near as each other, the day.
    let far = span.length - slot + 0.5;
    where.forEach((its, index) => {
      if (Math.abs(its - slot) >= far) return;
      best = index;
      far = Math.abs(its - slot);
    });
    return best;
  }

  let dragging = null;
  const drag = (e) => {
    const to = nearest(e.clientX);
    if (to === null) return;
    stop();
    choose(to);
    draw();
  };
  range.addEventListener('pointerdown', (e) => {
    if (e.button > 0 || nearest(e.clientX) === null) return;
    e.preventDefault();
    range.focus();
    range.setPointerCapture(e.pointerId);
    dragging = e.pointerId;
    drag(e);
  });
  range.addEventListener('pointermove', (e) => {
    if (dragging === e.pointerId) drag(e);
  });
  for (const type of ['pointerup', 'pointercancel', 'lostpointercapture']) {
    range.addEventListener(type, (e) => {
      if (dragging === e.pointerId) dragging = null;
    });
  }
  range.addEventListener('keydown', (e) => {
    const to = {
      ArrowLeft: at - 1, ArrowDown: at - 1, ArrowRight: at + 1, ArrowUp: at + 1,
      PageDown: at - LEAP, PageUp: at + LEAP, Home: 0, End: days.length,
    }[e.key];
    if (to === undefined) return;
    e.preventDefault();
    stop();
    choose(to);
    draw();
  });

  open.addEventListener('click', () => {
    unfold(true);
    draw();
    date.focus();
  });
  close.addEventListener('click', () => {
    shut();
    open.focus();
  });
  earlier.addEventListener('click', () => { stop(); choose(at - 1); draw(); });
  later.addEventListener('click', () => { stop(); choose(at + 1); draw(); });
  now.addEventListener('click', () => { stop(); choose(days.length); draw(); });
  play.addEventListener('click', () => {
    if (timer !== null) {
      stop();
      draw();
      return;
    }
    if (days.length === 0) return;
    // From now there is nowhere later to run to, so it starts over.
    if (at >= days.length) choose(0);
    run();
    draw();
  });
  paces.forEach((choice, i) => choice.addEventListener('click', () => hurry(SPEEDS[i])));
  // The arrow keys move among the speeds, as among radio buttons.
  speeds.addEventListener('keydown', (e) => {
    const by = { ArrowLeft: -1, ArrowUp: -1, ArrowRight: 1, ArrowDown: 1 }[e.key];
    if (!by) return;
    e.preventDefault();
    const to = (SPEEDS.indexOf(speed) + by + SPEEDS.length) % SPEEDS.length;
    hurry(SPEEDS[to]);
    paces[to].focus();
  });
  pace.addEventListener('click', () => hurry(SPEEDS[(SPEEDS.indexOf(speed) + 1) % SPEEDS.length]));
  loop.addEventListener('click', () => {
    looped = !looped;
    keep();
    draw();
  });
  more.addEventListener('click', () => {
    const whole = note.classList.toggle('whole');
    more.textContent = whole ? 'Less' : 'More';
    more.setAttribute('aria-expanded', String(whole));
  });

  // What a day's note says depends on the dimension shown, which may be
  // what has changed.
  document.addEventListener('mcmap:view', () => {
    draw();
    ask();
  });
  document.addEventListener('mcmap:day', follow);
  // Another tab, a file or a saved view may have changed how a play goes.
  document.addEventListener('mcmap:settings', (e) => {
    if (!e.detail || !e.detail.sections.includes('history')) return;
    const was = speed;
    recall();
    if (timer !== null && speed !== was) run();
    draw();
  });
  recall();
  ask();
})();
