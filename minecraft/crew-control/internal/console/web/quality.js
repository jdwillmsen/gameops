// The camera's quality levels as the console names them in q=, cheapest first.
// The camera page owns the full cost table; these are the level names it
// accepts, and the camera's quality test fails when the two lists drift apart.
export const QUALITIES = ['low', 'med', 'high'];
export const DEFAULT_QUALITY = 'high';

// Auto lets the wall governor pick; the rest pin a level for every live feed.
export const WALL_QUALITY_CHOICES = ['auto', 'low', 'med', 'high'];

export const qualityOf = q => (QUALITIES.includes(q) ? q : DEFAULT_QUALITY);

const LABELS = { auto: 'Auto', low: 'Low', med: 'Med', high: 'High' };
export const wallQualityLabel = q => LABELS[q] ?? LABELS.auto;
