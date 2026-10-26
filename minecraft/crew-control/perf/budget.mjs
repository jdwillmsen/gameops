// Console performance budget: starts crew-control against 20 simulated units,
// opens the console in headless Chromium and measures each scenario in
// budget.json. Exits 1 when any limit is broken.
//
//   node minecraft/crew-control/perf/budget.mjs [--scenario=wall-all] [--seconds=15]
//        [--json=out.json] [--cpu-throttle=4] [--url=http://host:port]   (--url skips starting a server)
//
// Needs Go and Playwright (PLAYWRIGHT can point at its entry module).
import { spawn, execFileSync } from 'node:child_process';
import { readFile, writeFile, mkdtemp } from 'node:fs/promises';
import { tmpdir } from 'node:os';
import { join, dirname, resolve } from 'node:path';
import { fileURLToPath } from 'node:url';
import { startMock } from './mockunits.mjs';
import { summarizeFrames, longestTask, check, round } from './budget-lib.mjs';

const here = dirname(fileURLToPath(import.meta.url));
const args = Object.fromEntries(process.argv.slice(2).map(a => a.replace(/^--/, '').split('=')).map(([k, v]) => [k, v ?? true]));
const seconds = Number(args.seconds || 15);
const budget = JSON.parse(await readFile(join(here, 'budget.json'), 'utf8'));
const names = args.scenario ? [args.scenario] : Object.keys(budget.scenarios);

const SCENARIOS = {
  // Prefs are what the console reads from localStorage; budget 999 is "All".
  'wall-all': { path: '/?view=wall', prefs: { budget: 999, side: true, dock: true }, ready: ['.wallgrid .tile iframe', 20] },
  fleet: { path: '/?view=fleet', prefs: { budget: 0, side: false, dock: true }, ready: ['.ov-tile', 1] },
};

async function startServer() {
  const mock = await startMock({ units: 20 });
  const dir = await mkdtemp(join(tmpdir(), 'crew-perf-'));
  const bin = join(dir, 'crew-control');
  const root = resolve(here, '../../..');
  execFileSync('go', ['build', '-o', bin, './minecraft/crew-control/cmd/crew-control'], { cwd: root, stdio: 'inherit', env: { ...process.env, CGO_ENABLED: '0' } });
  const port = 20000 + Math.floor(Math.random() * 20000);
  const child = spawn(bin, [], {
    env: { PATH: process.env.PATH, CREW_UNITS: JSON.stringify(mock.units), LISTEN_ADDR: `127.0.0.1:${port}`, DATA_DIR: dir, LOG_LEVEL: 'error' },
    stdio: ['ignore', 'inherit', 'inherit'],
  });
  const url = `http://127.0.0.1:${port}`;
  for (let i = 0; i < 100; i++) {
    try { if ((await fetch(`${url}/healthz`)).ok) break; } catch { /* not listening yet */ }
    await new Promise(r => setTimeout(r, 100));
  }
  return { url, stop: () => { child.kill(); mock.close(); } };
}

const step = m => { if (args.verbose) console.error(`[${new Date().toISOString().slice(11, 19)}] ${m}`); };
const pw = await import(process.env.PLAYWRIGHT || 'playwright');
const chromium = pw.chromium || pw.default.chromium;
step('starting');
const srv = args.url ? { url: args.url, stop() {} } : await startServer();
const results = {};
let failed = false;
let browser;

try {
  step('server up');
  browser = await chromium.launch({ args: ['--js-flags=--expose-gc'] });
  for (const name of names) {
    const sc = SCENARIOS[name];
    if (!sc) throw new Error(`unknown scenario ${name}`);
    const ctx = await browser.newContext({ viewport: { width: 1600, height: 900 } });
    const page = await ctx.newPage();
    await page.addInitScript(({ prefs, ready }) => {
      localStorage.setItem('crew-console-prefs', JSON.stringify(prefs));
      window.__frames = [];
      window.__tasks = [];
      window.__ready = null;
      let last = 0;
      const tick = t => { if (last) window.__frames.push(t - last); last = t; requestAnimationFrame(tick); };
      requestAnimationFrame(tick);
      try { new PerformanceObserver(l => { for (const e of l.getEntries()) window.__tasks.push({ at: e.startTime, duration: e.duration }); }).observe({ type: 'longtask', buffered: true }); } catch { /* unsupported */ }
      const check = () => { try { if (window.__ready === null && document.querySelectorAll(ready[0]).length >= ready[1]) window.__ready = performance.now(); } catch { /* DOM not there yet */ } };
      new MutationObserver(check).observe(document, { childList: true, subtree: true });
    }, { prefs: sc.prefs, ready: sc.ready });

    const cdp = await ctx.newCDPSession(page);
    await cdp.send('Network.enable');
    if (args['cpu-throttle']) await cdp.send('Emulation.setCPUThrottlingRate', { rate: Number(args['cpu-throttle']) });
    const sseIds = new Set();
    let sseBytes = 0;
    cdp.on('Network.requestWillBeSent', e => { if (e.request.url.includes('/v1/units/stream')) sseIds.add(e.requestId); });
    cdp.on('Network.dataReceived', e => { if (sseIds.has(e.requestId)) sseBytes += e.dataLength; });

    step('goto');
    await page.goto(srv.url + sc.path);
    await page.waitForFunction(() => window.__ready !== null, null, { timeout: 30000 });
    step('ready');
    const ttiMs = await page.evaluate(() => window.__ready);

    // Let the first poll burst and the cameras settle before measuring.
    await page.waitForTimeout(3000);
    await page.evaluate(() => { window.__frames.length = 0; window.__tasks.length = 0; });
    const from = await page.evaluate(() => performance.now());
    const bytes0 = sseBytes;
    await page.waitForTimeout(seconds * 1000);
    const frames = await page.evaluate(() => window.__frames.slice());
    const tasks = (await page.evaluate(() => window.__tasks.slice())).filter(t => t.at >= from);
    const sseRate = (sseBytes - bytes0) / seconds;

    await cdp.send('HeapProfiler.collectGarbage').catch(() => {});
    await cdp.send('Performance.enable');
    const metrics = Object.fromEntries((await cdp.send('Performance.getMetrics')).metrics.map(x => [x.name, x.value]));

    // The console sheds cameras on its own when slow; a run that lost any is not a pass.
    const liveFeeds = await page.evaluate(() => document.querySelectorAll('.wallgrid .tile iframe').length);
    const f = summarizeFrames(frames);
    const measured = {
      fps: f.fps,
      p95FrameMs: f.p95FrameMs,
      maxFrameMs: f.maxFrameMs,
      ttiMs,
      heapMb: (metrics.JSHeapUsedSize || 0) / 1048576,
      domNodes: metrics.Nodes,
      sseBytesPerSecPerUnit: sseRate / 20,
      eventLongTaskMs: longestTask(tasks),
      longTasks: tasks.length,
      ...(budget.scenarios[name].limits.liveFeedsMin ? { liveFeeds } : {}),
    };
    const problems = check(measured, budget.scenarios[name].limits);
    results[name] = { measured: Object.fromEntries(Object.entries(measured).map(([k, v]) => [k, round(v ?? 0)])), problems };
    console.log(`\n${name}: ${budget.scenarios[name].describe}`);
    for (const [k, v] of Object.entries(results[name].measured)) console.log(`  ${k.padEnd(24)} ${v}`);
    console.log(problems.length ? problems.map(p => `  FAIL ${p}`).join('\n') : '  within budget');
    if (problems.length) failed = true;
    await ctx.close();
  }
} finally {
  await browser?.close();
  srv.stop();
}
if (args.json) await writeFile(args.json, JSON.stringify(results, null, 2));
process.exit(failed ? 1 : 0);
