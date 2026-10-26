// Pure maths for the performance budget run, kept apart from the browser
// driver so the pass/fail rules are unit-tested without a browser.

export function summarizeFrames(deltas) {
  const d = deltas.filter(x => x > 0);
  if (!d.length) return { frames: 0, fps: 0, p95FrameMs: 0, maxFrameMs: 0, over50: 0 };
  const total = d.reduce((a, b) => a + b, 0);
  const sorted = [...d].sort((a, b) => a - b);
  return {
    frames: d.length,
    // Frames over elapsed time rather than the mean delta, so a few huge
    // stalls pull the figure down the way a viewer sees it.
    fps: (d.length / total) * 1000,
    p95FrameMs: sorted[Math.min(sorted.length - 1, Math.ceil(sorted.length * 0.95) - 1)],
    maxFrameMs: sorted[sorted.length - 1],
    over50: d.filter(x => x > 50).length,
  };
}

// Longest main-thread task inside a window, from PerformanceObserver entries.
export const longestTask = tasks => tasks.reduce((m, t) => Math.max(m, t.duration), 0);

const RULES = [
  ['fps', 'fpsMin', (v, l) => v >= l],
  ['p95FrameMs', 'p95FrameMsMax', (v, l) => v <= l],
  ['ttiMs', 'ttiMsMax', (v, l) => v <= l],
  ['heapMb', 'heapMbMax', (v, l) => v <= l],
  ['sseBytesPerSecPerUnit', 'sseBytesPerSecPerUnitMax', (v, l) => v <= l],
  ['liveFeeds', 'liveFeedsMin', (v, l) => v >= l],
  ['eventLongTaskMs', 'eventLongTaskMsMax', (v, l) => v <= l],
];

// Returns one message per limit that is missing a measurement or is broken.
export function check(measured, limits) {
  const out = [];
  for (const [metric, limit, ok] of RULES) {
    if (!(limit in limits)) continue;
    const v = measured[metric];
    if (typeof v !== 'number' || Number.isNaN(v)) out.push(`${metric}: not measured`);
    else if (!ok(v, limits[limit])) out.push(`${metric} ${round(v)} breaks ${limit} ${limits[limit]}`);
  }
  return out;
}

export const round = v => Math.round(v * 10) / 10;
