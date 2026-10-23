const SVG = 'http://www.w3.org/2000/svg';

function apply(el, attrs) {
  if (!attrs) return el;
  for (const k in attrs) {
    const v = attrs[k];
    if (v == null || v === false) continue;
    if (k === 'text') el.textContent = v;
    else el.setAttribute(k, v === true ? '' : v);
  }
  return el;
}

export function h(tag, attrs, ...kids) {
  const el = apply(document.createElement(tag), attrs);
  if (kids.length) el.append(...kids);
  return el;
}

export function s(tag, attrs, ...kids) {
  const el = apply(document.createElementNS(SVG, tag), attrs);
  if (kids.length) el.append(...kids);
  return el;
}

let counter = 0;
export const uid = () => `cc${(++counter).toString(36)}`;
