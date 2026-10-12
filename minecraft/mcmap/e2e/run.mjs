#!/usr/bin/env node
// Runs the browser checks against a map: the local harness, or a live one
// with a service session. See "Browser checks" in ../README.md.

import { mkdir, readdir, rm, stat, writeFile } from 'node:fs/promises';
import { readFileSync } from 'node:fs';
import path from 'node:path';
import { parseArgs } from 'node:util';
import { chromium } from 'playwright-core';

import { Watch, openContext, openMap } from './lib/browser.mjs';
import { Fail } from './lib/check.mjs';
import { FAIL, PASS, Report, SKIP } from './lib/report.mjs';
import { exchange, portForward, redact } from './lib/session.mjs';
import * as access from './checks/access.mjs';
import * as map from './checks/map.mjs';
import * as live from './checks/live.mjs';

const VERSION = process.env.MCMAP_E2E_VERSION || JSON.parse(readFileSync(new URL('./package.json', import.meta.url), 'utf8')).version;

// Every check, in the order they run. The two that the rest stand on are
// run whatever --only says.
const CHECKS = ['login-screen', 'exchange', 'map-loads', 'session', 'scope', 'headers', 'scripts', 'api-budget', 'dimensions', 'player-heads', 'zoom-sizes',
  'layer-panel', 'toggles', 'search', 'structure-sheet', 'saved-views', 'themes', 'follow-player', 'follow-mob', 'faces', 'phone-sheet', 'live-stream',
  'page-errors', 'read-only'];
const NEEDED = ['exchange', 'map-loads'];

const OPTIONS = {
  'public-url': { type: 'string', env: 'MCMAP_E2E_PUBLIC_URL' },
  'internal-url': { type: 'string', env: 'MCMAP_E2E_INTERNAL_URL' },
  'secret-file': { type: 'string', env: 'MCMAP_E2E_SECRET_FILE' },
  'secret-from-kube': { type: 'string' },
  'port-forward': { type: 'string' },
  'approved-by': { type: 'string' },
  'insecure-local-tls': { type: 'boolean' },
  'metrics-file': { type: 'string', env: 'MCMAP_E2E_METRICS_FILE' },
  'evidence-dir': { type: 'string', env: 'MCMAP_E2E_EVIDENCE_DIR' },
  'evidence-keep': { type: 'string', env: 'MCMAP_E2E_EVIDENCE_KEEP', default: '5' },
  'evidence-days': { type: 'string', env: 'MCMAP_E2E_EVIDENCE_DAYS', default: '7' },
  'stream-seconds': { type: 'string', env: 'MCMAP_E2E_STREAM_SECONDS', default: '125' },
  'stream-gap-seconds': { type: 'string', env: 'MCMAP_E2E_STREAM_GAP_SECONDS', default: '10' },
  'follow-mob-seconds': { type: 'string', env: 'MCMAP_E2E_FOLLOW_MOB_SECONDS', default: '60' },
  'follow-player-seconds': { type: 'string', env: 'MCMAP_E2E_FOLLOW_PLAYER_SECONDS', default: '20' },
  'follow-tolerance-px': { type: 'string', env: 'MCMAP_E2E_FOLLOW_TOLERANCE_PX', default: '4' },
  'api-budget-ms': { type: 'string', env: 'MCMAP_E2E_API_BUDGET_MS', default: '3000' },
  only: { type: 'string', env: 'MCMAP_E2E_ONLY' },
  help: { type: 'boolean' },
};

const USAGE = `usage: node run.mjs --public-url <https://map> --internal-url <http://map-internal> --secret-file <path>
       node run.mjs --public-url <https://map> --secret-from-kube <namespace/secret/key> --port-forward <namespace/service:port> --approved-by <who>

  The secret is read from a file or from the cluster, by this process and into its memory.
  There is no way to give it as an argument or in a variable.

  --insecure-local-tls     accept the harness's own certificate; refused for any address but this machine's
  --metrics-file <path>    write the run as Prometheus text
  --evidence-dir <path>    on a failed check, keep a screenshot, a trace and the page's own errors here.
                           They show the live map. Off unless given; never a directory that is committed or uploaded
  --only a,b               run only these checks
  durations and budgets: --stream-seconds --stream-gap-seconds --follow-mob-seconds --follow-player-seconds
                         --follow-tolerance-px --api-budget-ms --evidence-keep --evidence-days
  Each option but the two kube ones may be set as MCMAP_E2E_<NAME> instead.

  exit 0: every check passed or had nothing to look at; 1: a check failed; 2: the run itself could not be made
`;

function configure(argv) {
  const { values } = parseArgs({ args: argv, options: Object.fromEntries(Object.entries(OPTIONS).map(([name, { type }]) => [name, { type }])) });
  if (values.help) return { help: true };
  const get = (name) => values[name] ?? (OPTIONS[name].env ? process.env[OPTIONS[name].env] : undefined) ?? OPTIONS[name].default;
  const number = (name, least, most) => {
    const n = Number(get(name));
    if (!Number.isFinite(n) || n < least || n > most) throw new Error(`--${name} must be between ${least} and ${most}`);
    return n;
  };
  const publicUrl = String(get('public-url') || '').replace(/\/+$/, '');
  if (!/^https:\/\/[^/]+$/.test(publicUrl)) throw new Error('--public-url must be the https address of the map, with no path');
  const local = ['127.0.0.1', 'localhost', '[::1]'].includes(new URL(publicUrl).hostname);
  const external = Boolean(values['secret-from-kube'] || values['port-forward']);
  const cfg = {
    publicUrl,
    internalUrl: get('internal-url'),
    secretFile: get('secret-file'),
    secretRef: values['secret-from-kube'],
    forward: values['port-forward'],
    approvedBy: values['approved-by'],
    insecureLocalTls: values['insecure-local-tls'] === true,
    mode: local ? 'local' : external ? 'external' : 'cluster',
    metricsFile: get('metrics-file'),
    evidenceDir: get('evidence-dir'),
    evidenceKeep: number('evidence-keep', 1, 50),
    evidenceDays: number('evidence-days', 1, 30),
    streamSeconds: number('stream-seconds', 10, 280),
    streamGapSeconds: number('stream-gap-seconds', 3, 25),
    followMobSeconds: number('follow-mob-seconds', 5, 180),
    followPlayerSeconds: number('follow-player-seconds', 5, 180),
    followTolerancePx: number('follow-tolerance-px', 1, 50),
    apiBudgetMs: number('api-budget-ms', 100, 30_000),
    faceDensities: [1, 1.25, 1.5, 2],
    only: get('only') ? String(get('only')).split(',').map((name) => name.trim()).filter(Boolean) : null,
  };
  // A name that is no check's would run nothing and report a clean run.
  for (const name of cfg.only || []) if (!CHECKS.includes(name)) throw new Error(`--only names ${JSON.stringify(name)}, which is no check; the checks are ${CHECKS.join(', ')}`);
  if (cfg.insecureLocalTls && !local) throw new Error('--insecure-local-tls is for the harness on this machine and nothing else');
  if (Boolean(cfg.secretFile) === Boolean(cfg.secretRef)) throw new Error('give the secret one way: --secret-file, or --secret-from-kube');
  if (Boolean(cfg.internalUrl) === Boolean(cfg.forward)) throw new Error('give the internal listener one way: --internal-url, or --port-forward');
  // A run from outside the cluster against a live map is the owner's to
  // allow each time. Who allowed it is asked for and sent with the
  // exchange, where the map logs it; that is a record, not a lock.
  if (external && !/^[a-z0-9][a-z0-9._-]{1,31}$/i.test(cfg.approvedBy || '')) throw new Error('a run from outside the cluster needs --approved-by <who allowed it>');
  // Said in every request, so that the map's logs and the gateway's can
  // tell a check from a visitor.
  cfg.agent = `mcmap-e2e/${VERSION} (service:e2e; ${cfg.mode}${external ? `; approved-by ${cfg.approvedBy}` : ''})`;
  return cfg;
}

// Evidence shows the live map, so only so much of it is kept and only for
// so long: the newest few failed runs, and none older than the limit.
async function prune(dir, keep, days) {
  await mkdir(dir, { recursive: true, mode: 0o700 });
  const runs = [];
  for (const name of await readdir(dir)) {
    if (!/^run-\d{8}T\d{6}Z$/.test(name)) continue;
    runs.push({ name, at: (await stat(path.join(dir, name))).mtimeMs });
  }
  runs.sort((a, b) => b.at - a.at);
  const oldest = Date.now() - days * 86_400_000;
  // One fewer than the limit, to make room for this run's.
  for (const [n, run] of runs.entries()) if (n >= keep - 1 || run.at < oldest) await rm(path.join(dir, run.name), { recursive: true, force: true });
}

async function main() {
  let cfg;
  try {
    cfg = configure(process.argv.slice(2));
  } catch (err) {
    process.stderr.write(`${err.message}\n\n${USAGE}`);
    return 2;
  }
  if (cfg.help) { process.stdout.write(USAGE); return 0; }

  const report = new Report({ mode: cfg.mode, version: VERSION });
  const watch = new Watch();
  const runDir = cfg.evidenceDir ? path.join(cfg.evidenceDir, `run-${new Date().toISOString().replace(/[-:]|\.\d+/g, '')}`) : null;
  if (cfg.evidenceDir) await prune(cfg.evidenceDir, cfg.evidenceKeep, cfg.evidenceDays);
  report.log('start', { mode: cfg.mode, version: VERSION, evidence: Boolean(runDir) });

  let browser;
  let forwarded;
  const kept = [];
  // One check: its verdict, how long it took, and what it measured. What
  // went wrong in full goes to the evidence directory when there is one,
  // and to the log only as the check's own words.
  const run = async (name, fn, { page = null, ctx = null, seconds = 120 } = {}) => {
    if (!CHECKS.includes(name)) throw new Error(`${name} is not in the list of checks`);
    if (cfg.only && !cfg.only.includes(name) && !NEEDED.includes(name)) return null;
    const began = Date.now();
    if (runDir && ctx) await ctx.tracing.startChunk({ name }).catch(() => {});
    let timer;
    let result;
    try {
      const got = await Promise.race([fn(), new Promise((_, reject) => { timer = setTimeout(() => reject(new Fail(`not finished in ${seconds} s`)), seconds * 1000); })]) || {};
      result = got.skip ? report.record(name, SKIP, Date.now() - began, got.skip, got.measures) : report.record(name, PASS, Date.now() - began, got.detail || '', got.measures);
    } catch (err) {
      const known = err instanceof Fail;
      result = report.record(name, FAIL, Date.now() - began, known ? err.message : `the check itself broke: ${err.name}`, known ? err.measures : {});
      kept.push({ check: name, error: redact(String(err.stack || err)) });
      if (runDir) {
        await mkdir(runDir, { recursive: true, mode: 0o700 });
        if (page && !page.isClosed()) await page.screenshot({ path: path.join(runDir, `${name}.png`) }).catch(() => {});
        if (ctx) await ctx.tracing.stopChunk({ path: path.join(runDir, `${name}-trace.zip`) }).catch(() => {});
      }
    } finally {
      clearTimeout(timer);
      if (runDir && ctx && result.status !== FAIL) await ctx.tracing.stopChunk().catch(() => {});
    }
    return result;
  };

  try {
    // A container gives /dev/shm 64 MB, which a page of canvases outgrows;
    // the browser is told to use its temporary directory instead.
    browser = await chromium.launch({ executablePath: process.env.MCMAP_E2E_CHROMIUM || undefined, args: ['--disable-dev-shm-usage'] });
    // The browser's own, with who this is on the end of it.
    const blank = await browser.newContext();
    const plain = await (await blank.newPage()).evaluate(() => navigator.userAgent);
    await blank.close();
    cfg.userAgent = `${plain} ${cfg.agent}`;

    await run('login-screen', () => access.loginScreen({ browser, cfg, watch }));

    const held = await run('exchange', async () => {
      if (cfg.forward) {
        forwarded = await portForward(cfg.forward);
        cfg.internalUrl = forwarded.url;
      }
      cfg.session = await exchange({ internalUrl: cfg.internalUrl, publicUrl: cfg.publicUrl, userAgent: cfg.agent, secretFile: cfg.secretFile, secretRef: cfg.secretRef });
      // The forward was for the exchange alone: everything after goes
      // the way a visitor's browser does.
      if (forwarded) { forwarded.close(); forwarded = null; }
      return { measures: { ttl_seconds: cfg.session.ttlSeconds } };
    }, { seconds: 60 });

    if (cfg.session) {
      const ctx = await openContext(browser, { cfg, watch, cookie: cfg.session.cookie });
      if (runDir) await ctx.tracing.start({ snapshots: true, screenshots: false });
      const t = { browser, ctx, cfg, watch, report };
      // The watcher is opened first and then left alone: it is the
      // stream that page holds that the last check asks about.
      const loaded = await run('map-loads', async () => {
        const began = Date.now();
        t.watcher = await openMap(ctx, cfg);
        t.page = await openMap(ctx, cfg);
        return { measures: { load_ms: Math.round((Date.now() - began) / 2) } };
      }, { ctx, seconds: 90 });
      if (loaded && loaded.status !== PASS) throw new Fail('the map did not load with a session, so nothing on it was checked');
      const { page, watcher } = t;
      const on = { page, ctx };
      await run('session', () => access.session(t), on);
      await run('scope', () => access.scope(t), on);
      await run('headers', () => access.headers(t), on);
      await run('scripts', () => access.scripts(t), on);
      await run('api-budget', () => access.apiBudget(t), on);
      await run('dimensions', () => map.dimensions(t), on);
      await run('player-heads', () => live.playerHeads(t), on);
      await run('zoom-sizes', () => map.zoomSizes(t), on);
      await run('layer-panel', () => map.layerPanel(t), on);
      await run('toggles', () => map.toggles(t), on);
      await run('search', () => map.search(t), on);
      await run('structure-sheet', () => map.structureSheet(t), on);
      await run('saved-views', () => map.savedViews(t), on);
      await run('themes', () => map.themes(t), on);
      await run('follow-player', () => live.followPlayer(t), { ...on, seconds: cfg.followPlayerSeconds + 60 });
      await run('follow-mob', () => live.followMob(t), { ...on, seconds: cfg.followMobSeconds + 60 });
      await run('faces', () => live.faces(t), { seconds: 180 });
      await run('phone-sheet', () => map.phoneSheet(t));
      await run('live-stream', () => live.liveStream(t), { page: watcher, ctx, seconds: cfg.streamSeconds + 90 });
      if (runDir) await ctx.tracing.stop().catch(() => {});
      await ctx.close();
    } else if (held) {
      report.log('stopped', { why: 'no session, so nothing behind the login was checked' });
    }

    // Over the whole run, in every page it opened.
    await run('page-errors', async () => {
      const counts = watch.counts();
      const measures = { page_errors: counts.page_errors, failed_responses: counts.failed_responses, failed_requests: counts.failed_requests };
      if (counts.page_errors > 0) throw new Fail(`the page's scripts threw: ${[...new Set(watch.errors)].slice(0, 5).join(', ')}`, measures);
      if (counts.failed_responses > 0) throw new Fail(`refused or broken answers: ${[...watch.responses.keys()].slice(0, 5).join(', ')}`, measures);
      if (counts.failed_requests > 0) throw new Fail(`requests that never got an answer: ${[...watch.failures.keys()].slice(0, 5).join(', ')}`, measures);
      return { measures };
    });
    await run('read-only', async () => {
      if (watch.writes.length > 0) throw new Fail(`the page tried to write, and was stopped: ${[...new Set(watch.writes)].slice(0, 5).join(', ')}`, { writes: watch.writes.length });
      return { measures: { writes: 0 } };
    });
  } catch (err) {
    if (err instanceof Fail) report.log('stopped', { why: err.message });
    else {
      report.record('run', FAIL, Date.now() - report.started, `the run itself broke: ${err.name}`);
      kept.push({ check: 'run', error: redact(String(err.stack || err)) });
    }
  } finally {
    if (forwarded) forwarded.close();
    if (browser) await browser.close().catch(() => {});
  }

  if (runDir && (kept.length > 0 || watch.raw.length > 0) && report.failed > 0) {
    await mkdir(runDir, { recursive: true, mode: 0o700 });
    await writeFile(path.join(runDir, 'failures.json'), redact(JSON.stringify({ failed: kept, seen: watch.raw }, null, 1)), { mode: 0o600 });
  }
  report.summary();
  if (cfg.metricsFile) await report.writeMetrics(cfg.metricsFile);
  if (report.results.some((r) => r.check === 'run')) return 2;
  return report.failed > 0 ? 1 : 0;
}

process.exitCode = await main();
