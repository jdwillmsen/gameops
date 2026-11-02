// What each camera quality level costs. The console names a level in q; one
// the page does not know falls back to high, which is how every feed rendered
// before levels existed.
export const QUALITIES = ['low', 'med', 'high'];

// radius is the first/third-person view distance in chunks; orbit and top add
// two, as they always have. pixelRatio caps the device ratio, so a retina
// display still gets two at high. fps 0 leaves the page uncapped.
export const QUALITY = {
  low: { pixelRatio: .5, antialias: false, radius: 2, fps: 10, entities: false, items: false, hud: false },
  med: { pixelRatio: 1, antialias: false, radius: 3, fps: 20, entities: true, items: false, hud: false },
  high: { pixelRatio: 2, antialias: true, radius: 4, fps: 0, entities: true, items: true, hud: true },
};

export const DEFAULT_QUALITY = 'high';

export const qualityOf = q => (QUALITIES.includes(q) ? q : DEFAULT_QUALITY);
export const qualitySettings = q => QUALITY[qualityOf(q)];
