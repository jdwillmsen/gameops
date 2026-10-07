'use strict';

// A length of time as a viewer types one, and as it is said back to them.
// The live layer's pace and the trails' window are both chosen from a few
// presets or typed: "5", "90s", "1.5m", "1m30s", "2h", "1d". What was
// understood is always said in words before it is used, so that a slip is
// seen and not silently applied.
(() => {
  const app = window.mcmap;
  if (!app) return;

  const UNITS = [
    ['d', 86_400, ['d', 'day', 'days']],
    ['h', 3600, ['h', 'hr', 'hrs', 'hour', 'hours']],
    ['m', 60, ['m', 'min', 'mins', 'minute', 'minutes']],
    ['s', 1, ['s', 'sec', 'secs', 'second', 'seconds']],
  ];
  const SECONDS = new Map();
  for (const [, seconds, words] of UNITS) for (const word of words) SECONDS.set(word, seconds);
  const MAX_TYPED = 32;
  const HOW = 'Type a number with s, m, h or d, such as 90s, 1m30s or 2h.';

  // A length in words: "1 min 30 s", "2 h", "1 day". To the second, which
  // is as fine as anything here is paced.
  function words(seconds) {
    let left = Math.max(0, Math.round(seconds));
    if (left === 0) return '0 s';
    const out = [];
    for (const [label, size] of [['day', 86_400], ['h', 3600], ['min', 60], ['s', 1]]) {
      const n = Math.floor(left / size);
      left -= n * size;
      if (n > 0) out.push(label === 'day' ? `${n} ${n === 1 ? 'day' : 'days'}` : `${n} ${label}`);
    }
    return out.join(' ');
  }

  // Reads what was typed. unit is what a bare number counts, as the
  // letter of one of the units. Returns { seconds } or { error }, the
  // error being a sentence for the viewer; what was typed is never part
  // of it.
  function parse(typed, unit = 's') {
    const text = String(typed ?? '').trim().toLowerCase();
    if (text === '') return { error: HOW };
    if (text.length > MAX_TYPED) return { error: `That is too long to be a length of time. ${HOW}` };
    if (/^[-−]/.test(text)) return { error: 'A length of time cannot be negative.' };
    const part = /\s*(\d+(?:\.\d+)?|\.\d+)\s*([a-z]*)\s*/y;
    let at = 0;
    let total = 0;
    let parts = 0;
    let bare = false;
    while (at < text.length) {
      part.lastIndex = at;
      const m = part.exec(text);
      if (!m) return { error: `That is not a length of time. ${HOW}` };
      at = part.lastIndex;
      parts += 1;
      const size = m[2] === '' ? SECONDS.get(unit) : SECONDS.get(m[2]);
      if (size === undefined) return { error: `That unit is not one this knows. ${HOW}` };
      bare = bare || m[2] === '';
      total += Number(m[1]) * size;
    }
    // "1m30" could be thirty of anything.
    if (bare && parts > 1) return { error: `A number is missing its unit. ${HOW}` };
    if (!Number.isFinite(total)) return { error: `That is not a length of time. ${HOW}` };
    return { seconds: total };
  }

  // What a typed length comes to between two bounds: { seconds, said,
  // problem }. said is the sentence shown under the box; problem is true
  // where nothing can be used. A length outside the bounds is brought to
  // the nearer one, and the sentence says so and why.
  function settle(typed, { unit, min, max, minWhy = '', maxWhy = '', say = (w) => w }) {
    const read = parse(typed, unit);
    if (read.error) return { seconds: null, said: read.error, problem: true };
    const rounded = Math.round(read.seconds);
    if (rounded < min) return { seconds: min, said: `The shortest is ${words(min)}${minWhy}, so: ${say(words(min))}.`, problem: false, clamped: true };
    if (rounded > max) return { seconds: max, said: `The longest is ${words(max)}${maxWhy}, so: ${say(words(max))}.`, problem: false, clamped: true };
    return { seconds: rounded, said: `${say(words(rounded))}`, problem: false };
  }

  let made = 0;

  // A control that chooses a length: a menu of presets, with one entry
  // that opens a box to type any other. name is what it is called to a
  // screen reader; presets are in seconds; min and max may be functions,
  // for a bound that is only known once the server has answered.
  function picker({ name, presets, unit = 's', min, max, minWhy, maxWhy, say, value, onChange }) {
    made += 1;
    const bound = (v) => (typeof v === 'function' ? v() : v);
    const limits = () => ({ unit, min: bound(min), max: bound(max), minWhy, maxWhy, say });
    let current = value;

    const node = document.createElement('span');
    node.className = 'duration';
    const select = document.createElement('select');
    select.setAttribute('aria-label', name);
    const box = document.createElement('input');
    box.type = 'text';
    box.hidden = true;
    box.maxLength = MAX_TYPED;
    box.autocomplete = 'off';
    box.spellcheck = false;
    box.placeholder = unit === 'h' ? '90m, 2h, 1d' : '90s, 1m30s, 2h';
    box.setAttribute('aria-label', `${name}: type a length of time`);
    const said = document.createElement('span');
    said.className = 'said';
    said.id = `duration-said-${made}`;
    said.setAttribute('role', 'status');
    box.setAttribute('aria-describedby', said.id);
    node.append(select, box, said);

    const tell = (text, problem) => {
      said.textContent = text;
      said.classList.toggle('problem', Boolean(problem));
      // Hung from whichever end of the control is further from the edge
      // of the page, so that it never reaches past it.
      said.classList.toggle('end', node.getBoundingClientRect().left > innerWidth / 2);
    };

    function options() {
      const list = [...new Set([...presets, current])].sort((a, b) => a - b);
      select.replaceChildren(...list.map((seconds) => {
        const option = document.createElement('option');
        option.value = String(seconds);
        option.textContent = words(seconds);
        return option;
      }));
      const other = document.createElement('option');
      other.value = 'other';
      other.textContent = 'Other…';
      select.append(other);
      select.value = String(current);
    }

    function typing(on) {
      box.hidden = !on;
      select.hidden = on;
      if (on) {
        box.value = '';
        tell(HOW, false);
        box.focus();
      }
    }

    function apply(seconds, why) {
      const changed = seconds !== current;
      current = seconds;
      options();
      typing(false);
      tell(why || '', false);
      if (changed && typeof onChange === 'function') onChange(seconds);
    }

    select.addEventListener('change', () => {
      if (select.value === 'other') {
        typing(true);
        return;
      }
      const seconds = Number(select.value);
      if (Number.isFinite(seconds)) apply(seconds);
    });
    box.addEventListener('input', () => {
      const read = settle(box.value, limits());
      tell(read.said, read.problem);
    });
    box.addEventListener('keydown', (e) => {
      if (e.key === 'Enter') {
        e.preventDefault();
        const read = settle(box.value, limits());
        if (read.problem) {
          tell(read.said, true);
          return;
        }
        // What was brought to a bound stays said: the number in the menu
        // is otherwise not the one that was typed, with no word of why.
        apply(read.seconds, read.clamped ? read.said : '');
        select.focus();
      } else if (e.key === 'Escape') {
        // Back to the menu as it was, and not also an Escape for the page.
        e.preventDefault();
        e.stopPropagation();
        apply(current);
        select.focus();
      }
    });
    box.addEventListener('blur', () => {
      if (box.hidden) return;
      const read = settle(box.value, limits());
      if (read.problem) apply(current);
      else apply(read.seconds, read.clamped ? read.said : '');
    });

    options();
    return {
      node,
      get value() { return current; },
      // Sets it without telling onChange, for a value read from storage
      // or brought inside a bound the server has just given.
      set(seconds, why) {
        current = seconds;
        options();
        if (why !== undefined) tell(why, false);
      },
    };
  }

  app.duration = { parse, words, settle, picker };
})();
