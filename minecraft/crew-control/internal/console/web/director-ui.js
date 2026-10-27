// The auto-director's DOM: a header toggle and a status-bar note. The
// choosing lives in director.js; here it only follows the clock and the feed.
import { mergeEvents } from './fleet.js';
import { createDirector, press, pause, tick, note } from './director.js';

const $ = id => document.getElementById(id);

// host: { statuses, order(), state(), follow(id) }
export function initDirector(host) {
  const d = createDirector();
  let feed = [];

  function render() {
    const btn = $('director-toggle');
    btn.setAttribute('aria-pressed', String(d.on && !d.paused));
    btn.dataset.state = !d.on ? 'off' : d.paused ? 'paused' : 'following';
    btn.title = d.on ? (d.paused ? 'Auto-director paused: resume (Shift-D)' : 'Auto-director on: turn off (Shift-D)') : 'Auto-director: follow the most interesting unit (Shift-D)';
    const s = host.statuses.get(d.unit);
    $('sb-director').textContent = note(d, s && s.name, Date.now());
  }

  function step() {
    const unit = tick(d, { feed, statuses: host.statuses, now: Date.now(), current: host.state().unit, order: host.order() });
    if (unit) host.follow(unit);
    render();
  }

  setInterval(step, 2000);
  $('director-toggle').addEventListener('click', toggle);

  function toggle() {
    press(d, Date.now());
    step();
  }

  return {
    toggle,
    // A focus the viewer chose pins the shot.
    manual() { if (d.on && !d.paused) { pause(d); render(); } },
    onEvents(list, live) {
      feed = mergeEvents(live ? feed : [], list, 200).feed;
      if (live) step();
    },
    isOn: () => d.on && !d.paused,
  };
}
