// Opens N headless camera pages spread across M crew units, waits for each to
// settle, then reports the page's own frame-cost stats over a fixed window.
//
// usage: node viewers.mjs --chrome PATH --ports 18100,18101 --viewers N
//          --radius R --window SECONDS [--settle-timeout SECONDS]
// Prints "SETTLED" when every page has meshed (the caller starts its own
// measurement window then) and one JSON line at the end.
import { createRequire } from 'node:module';

const arg = (name, fallback) => {
  const i = process.argv.indexOf(`--${name}`);
  return i > 0 ? process.argv[i + 1] : fallback;
};
// Resolved from NODE_PATH-style dirs so the script itself stays free of any
// machine-specific install location.
const require = createRequire(arg('pw-dir', process.cwd()) + '/');
const { chromium } = require('playwright-core');

const chrome = arg('chrome');
const ports = arg('ports', '').split(',').filter(Boolean);
const viewers = Number(arg('viewers', '1'));
const radius = Number(arg('radius', '4'));
const windowS = Number(arg('window', '30'));
const settleTimeoutS = Number(arg('settle-timeout', '120'));
if (!chrome || ports.length === 0) {
  console.error('need --chrome and --ports');
  process.exit(2);
}

const browser = await chromium.launch({
  executablePath: chrome,
  // Software GL: the lab host has no GPU, which is the worst case for a tile.
  args: ['--no-sandbox', '--use-angle=swiftshader', '--enable-unsafe-swiftshader'],
});
const pages = [];
for (let i = 0; i < viewers; i++) {
  const page = await browser.newPage({ viewport: { width: 640, height: 360 } });
  await page.goto(`http://127.0.0.1:${ports[i % ports.length]}/camera?r=${radius}`);
  pages.push(page);
}

const settled = await Promise.all(pages.map((p) =>
  p.waitForFunction(() => window.__stats.settledMs !== null, null, { timeout: settleTimeoutS * 1000 })
    .then(() => true, () => false)));
console.log('SETTLED');

const snap = () => Promise.all(pages.map((p) => p.evaluate(() => ({ ...window.__stats, t: performance.now() }))));
const before = await snap();
await new Promise((r) => setTimeout(r, windowS * 1000));
const after = await snap();

const per = after.map((a, i) => {
  const frames = a.frames - before[i].frames;
  return {
    settled: settled[i],
    settledMs: a.settledMs,
    firstFrameMs: a.firstFrameMs,
    fps: frames / ((a.t - before[i].t) / 1000),
    workMsAvg: frames ? (a.workMs - before[i].workMs) / frames : null,
    // The max is over the page's whole life, so it includes the replay burst.
    workMaxMs: a.workMaxMs,
    rxBytes: a.bytes,
  };
});
console.log(JSON.stringify(per));
await browser.close();
