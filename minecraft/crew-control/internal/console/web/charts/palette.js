// The categorical order is the colour-blind safety mechanism: validate any change
// with the dataviz validator for both surfaces before editing these tables, and
// keep charts.css in step (palette.test.mjs fails when they drift).

export const SURFACE = { light: '#fcfcfb', dark: '#1a1a19' };

export const SERIES = {
  light: ['#2a78d6', '#eb6834', '#1baf7a', '#eda100', '#e87ba4', '#008300', '#6250d6', '#e34948'],
  dark: ['#3987e5', '#d95926', '#199e70', '#c98500', '#d55181', '#008300', '#9085e9', '#e66767'],
};

export const MAX_SERIES = SERIES.light.length;

export const OTHER = '#898781';

// Reserved for state; a series never wears these.
export const STATUS = { good: '#0ca30c', warning: '#fab219', serious: '#ec835a', critical: '#d03b3b' };

// Seven magnitude steps, low to high. The dark ramp is the same hue reversed so
// "near zero" recedes toward the dark surface instead of glowing.
const BLUE = ['#cde2fb', '#9ec5f4', '#6da7ec', '#3987e5', '#256abf', '#184f95', '#0d366b'];
export const SEQUENTIAL = { light: BLUE, dark: [...BLUE].reverse() };

export const DIVERGING_MID = { light: '#f0efec', dark: '#383835' };

export const CHROME = {
  light: { ink: '#0b0b0b', ink2: '#52514e', muted: '#898781', grid: '#e1e0d9', axis: '#c3c2b7' },
  dark: { ink: '#f0efec', ink2: '#c3c2b7', muted: '#898781', grid: '#2c2c2a', axis: '#383835' },
};
