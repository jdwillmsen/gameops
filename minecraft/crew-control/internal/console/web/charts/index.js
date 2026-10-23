export { LineChart } from './line.js';
export { createTimeRange, mountRangeFilter } from './range.js';
export { createRegistry, defaultRegistry, foldOther } from './series.js';
export { compact, full } from './scale.js';
export { observeFrames, renderOffscreen } from './panel.js';

// Theme and texture are plain attributes read by charts.css, so they apply to
// every chart under the element at once, tooltips included.
export function setTheme(el, theme) {
  if (theme === 'auto') el.removeAttribute('data-theme');
  else el.setAttribute('data-theme', theme);
}

export function setTexture(el, on) {
  if (on) el.setAttribute('data-texture', 'on');
  else el.removeAttribute('data-texture');
}
