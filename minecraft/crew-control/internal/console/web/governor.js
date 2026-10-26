// Steps the wall's live-camera budget down when the page cannot hold a usable
// frame rate. The fixed budgets in fleet.js are chosen by a viewer on one
// machine; this keeps a weaker one from freezing under "All".

export const LOW_FPS = 24;
export const HOLD_MS = 4000;
// A frame this long means the tab was hidden or the machine slept, not that
// the page is slow.
const STALL_MS = 1000;

export function createGovernor({ lowFps = LOW_FPS, holdMs = HOLD_MS } = {}) {
  let start = 0;
  let frames = 0;
  let last = 0;
  return {
    reset() { start = 0; frames = 0; last = 0; },
    // Feed it each animation frame's timestamp; true means the average over
    // the last holdMs was below lowFps and the caller should shed load.
    frame(now) {
      if (last && now - last > STALL_MS) { start = 0; frames = 0; }
      last = now;
      if (!start) { start = now; frames = 0; return false; }
      frames++;
      const span = now - start;
      if (span < holdMs) return false;
      const fps = (frames / span) * 1000;
      start = now;
      frames = 0;
      return fps < lowFps;
    },
  };
}

// The next smaller live budget than current, never below the smallest
// non-zero one, or 0 when there is none: Cards is the viewer's choice.
export function lowerBudget(budgets, current, all) {
  const live = budgets.filter(b => b > 0).sort((a, b) => a - b);
  const smaller = live.filter(b => b < current && b < all);
  return smaller.length ? smaller[smaller.length - 1] : 0;
}
