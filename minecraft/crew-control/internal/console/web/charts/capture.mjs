// Screenshots and a frame-time run for the gallery page. Not shipped in the
// binary; needs Playwright (PLAYWRIGHT can point at its entry module).
//   node capture.mjs http://127.0.0.1:8080/static/charts/demo.html ./out [--perf=30]
import { mkdir, writeFile } from 'node:fs/promises';

const [url, out, ...flags] = process.argv.slice(2);
if (!url || !out) {
  console.error('usage: node capture.mjs <demo url> <out dir> [--perf=<seconds>]');
  process.exit(2);
}
const perfFlag = flags.find(f => f.startsWith('--perf'));
const { chromium } = await import(process.env.PLAYWRIGHT || 'playwright');
await mkdir(out, { recursive: true });
const browser = await chromium.launch();
const SECTIONS = ['tiles', 'lines', 'bars', 'heat', 'gauges', 'palette'];

async function open(mode, query = '') {
  const page = await browser.newPage({ viewport: { width: 1280, height: 900 }, colorScheme: mode });
  const errors = [];
  page.on('pageerror', e => errors.push(e.message));
  page.on('console', m => m.type() === 'error' && errors.push(m.text()));
  await page.goto(`${url}?theme=${mode}&eager=1${query}`);
  await page.waitForSelector('.cc-svg');
  await page.waitForTimeout(600);
  return { page, errors };
}

for (const mode of ['light', 'dark']) {
  const { page, errors } = await open(mode);
  await page.screenshot({ path: `${out}/gallery-${mode}.png`, fullPage: true });
  for (const id of SECTIONS) await page.locator(`section:has(#${id})`).screenshot({ path: `${out}/${id}-${mode}.png` });

  const line = page.locator('figure.cc-line').first();
  await line.scrollIntoViewIfNeeded();
  const lb = await line.locator('svg.cc-svg').boundingBox();
  await page.mouse.move(lb.x + lb.width * 0.55, lb.y + lb.height * 0.45);
  await page.waitForTimeout(150);
  await line.screenshot({ path: `${out}/hover-line-${mode}.png` });
  await page.screenshot({ path: `${out}/hover-line-tooltip-${mode}.png`, clip: { x: Math.max(0, lb.x - 20), y: Math.max(0, lb.y - 60), width: Math.min(1280, lb.width + 40), height: lb.height + 140 } });

  const bar = page.locator('figure.cc-bar').nth(3);
  await bar.scrollIntoViewIfNeeded();
  const bb = await bar.locator('svg.cc-svg').boundingBox();
  await page.mouse.move(bb.x + 90, bb.y + 90);
  await page.waitForTimeout(150);
  await page.screenshot({ path: `${out}/hover-stacked-bar-${mode}.png`, clip: { x: Math.max(0, bb.x - 20), y: Math.max(0, bb.y - 60), width: Math.min(1280, bb.width + 40), height: bb.height + 140 } });

  await page.mouse.move(0, 0);
  await line.locator('.cc-tbtn').click();
  await line.screenshot({ path: `${out}/table-view-${mode}.png` });
  await line.locator('.cc-tbtn').click();

  await line.scrollIntoViewIfNeeded();
  const kb = await line.locator('svg.cc-svg').boundingBox();
  await line.locator('svg.cc-svg').focus();
  await page.keyboard.press('ArrowLeft');
  await page.waitForTimeout(100);
  const area = { x: Math.max(0, kb.x - 20), y: Math.max(0, kb.y - 60), width: Math.min(1280, kb.width + 40), height: kb.height + 140 };
  await page.screenshot({ path: `${out}/keyboard-focus-${mode}.png`, clip: area });
  await line.locator('svg.cc-svg').evaluate(el => el.blur());
  await page.mouse.move(kb.x + 100, kb.y + 60);
  await page.mouse.down();
  await page.mouse.move(kb.x + 260, kb.y + 60, { steps: 6 });
  await page.screenshot({ path: `${out}/brushing-${mode}.png`, clip: area });
  await page.mouse.up();
  await page.waitForTimeout(150);
  await page.locator('section:has(#lines)').screenshot({ path: `${out}/zoomed-${mode}.png` });
  if (errors.length) throw new Error(`${mode}: ${errors.join('; ')}`);
  await page.close();
}

for (const mode of ['light', 'dark']) {
  const { page } = await open(mode, '&texture=1');
  await page.screenshot({ path: `${out}/gallery-texture-${mode}.png`, fullPage: true });
  await page.close();
}

if (perfFlag) {
  const seconds = Number(perfFlag.split('=')[1]) || 30;
  const results = {};
  for (const throttle of [1, 4]) {
    const { page } = await open('light', '&live=1');
    const cdp = await page.context().newCDPSession(page);
    await cdp.send('Emulation.setCPUThrottlingRate', { rate: throttle });
    await page.locator('#live').scrollIntoViewIfNeeded();
    // Restart so the counters cover only the throttled, on-screen run.
    await page.click('#live-toggle');
    await page.click('#live-toggle');
    await page.waitForTimeout(seconds * 1000);
    results[`cpu x${throttle}`] = await page.evaluate(() => {
      const s = window.__ccPerf;
      const pct = (a, p) => [...a].sort((x, y) => x - y)[Math.min(a.length - 1, Math.floor(a.length * p))];
      return { ticks: s.ticks, frames: s.frames, framesOver25ms: s.dropped, worstFrameMs: +s.worstFrame.toFixed(1), updateMsP50: +pct(s.tick, 0.5).toFixed(2), frameDrawMsP50: +pct(s.draw, 0.5).toFixed(2), frameDrawMsP95: +pct(s.draw, 0.95).toFixed(2), frameDrawMsMax: +Math.max(...s.draw).toFixed(2), framesToDrawAll20P95: pct(s.settle, 0.95), framesToDrawAll20Max: Math.max(...s.settle) };
    });
    await page.screenshot({ path: `${out}/live-cpu-x${throttle}.png` });
    await page.close();
  }
  await writeFile(`${out}/perf.json`, JSON.stringify(results, null, 2));
  console.log(JSON.stringify(results, null, 2));
}
await browser.close();
