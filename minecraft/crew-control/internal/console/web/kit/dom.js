// The one element builder for the console. Views used to carry twenty private
// copies that disagreed on which props become attributes; this fixes the rule
// in a single place and keeps text on textContent so nothing reaches innerHTML.

// Only these keys are attributes; every other key is a DOM property.
const ATTRIBUTE = /^(aria-|data-|role$)/;

function append(el, kids) {
  for (const k of kids) {
    if (Array.isArray(k)) append(el, k);
    else if (k != null && k !== false) el.append(typeof k === 'number' ? String(k) : k);
  }
  return el;
}

// h('button', { class: 'x', 'aria-pressed': false, on: { click } }, 'Go')
export function h(tag, props, ...kids) {
  const el = document.createElement(tag);
  let value;
  for (const key in props) {
    const v = props[key];
    if (v == null) continue;
    if (key === 'on') for (const type in v) el.addEventListener(type, v[type]);
    else if (key === 'class') el.className = v;
    else if (key === 'text') el.textContent = v;
    else if (key === 'value') value = v;
    else if (ATTRIBUTE.test(key)) el.setAttribute(key, String(v));
    else el[key] = v;
  }
  append(el, kids);
  // A select ignores a value that names an option it does not have yet.
  if (value !== undefined) el.value = value;
  return el;
}

export const FOCUS_KEY = 'data-focus-key';

// Redrawing by replaceChildren drops focus, which a live feed does every few
// seconds. Elements that carry a focus key get focus back when the redraw puts
// an element with the same key in the tree.
export function keepFocus(root, redraw) {
  const active = document.activeElement;
  const key = active && root.contains(active) && active.getAttribute(FOCUS_KEY);
  redraw();
  if (!key) return;
  const next = Array.from(root.querySelectorAll(`[${FOCUS_KEY}]`)).find(e => e.getAttribute(FOCUS_KEY) === key);
  if (next) next.focus();
}
