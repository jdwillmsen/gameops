#!/usr/bin/env node
// Runs the browser checks against a map: the local harness, or a live one
// with a service session. See "Browser checks" in ../README.md.

import { readFileSync } from 'node:fs';
import path from 'node:path';
import { parseArgs } from 'node:util';
import { chromium } from 'playwright-core';

import { Watch, openContext, openMap } from './lib/browser.mjs';
import { Fail, within } from './lib/check.mjs';
import { CHECKS as LISTED, DENSITIES } from './lib/fields.mjs';
import { FAIL, PASS, Report, SKIP } from './lib/report.mjs';
import { Evidence, prune } from './lib/evidence.mjs';
import { ExchangeError, exchange, portForward } from './lib/session.mjs';
import * as access from './checks/access.mjs';
import * as map from './checks/map.mjs';
import * as live from './checks/live.mjs';

const VERSION = process.env.MCMAP_E2E_VERSION || JSON.parse(readFileSync(new URL('./package.json', import.meta.url), 'utf8')).version;

// Every check a run may be asked for by name. The two that the rest stand
// on are run whatever --only says.
const CHECKS = LISTED.filter((name) => name !== 'run');
const NEEDED = ['exchange', 'map-loads'];

const OPTIONS = {
  'public-url': { type: 'string', env: 'MCMAP_E2E_PUBLIC_URL' },
  'internal-url': { type: 'string', env: 'MCMAP_E2E_INTERNAL_URL' },
  'secret-file': { type: 'string', env: 'MCMAP_E2E_SECRET_FILE' },
  'secret-from-kube': { type: 'string' },
  'port-forward': { type: 'string' },
  'approved-by': { type: 'string' },
  'insecure-local-tls': { type: 'boolean' },
  'no-history': { type: 'boolean', env: 'MCMAP_E2E_NO_HISTORY' },
  'metrics-file': { type: 'string', env: 'MCMAP_E2E_METRICS_FILE' },
  'evidence-dir': { type: 'string', env: 'MCMAP_E2E_EVIDENCE_DIR' },
  'evidence-pages': { type: 'boolean', env: 'MCMAP_E2E_EVIDENCE_PAGES' },
  'evidence-max-mb': { type: 'string', env: 'MCMAP_E2E_EVIDENCE_MAX_MB', default: '20' },
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
  --no-history             the map keeps no history, so its 404 to the page's one ask for the days kept is
                           no fault. Without this that answer fails the run, as it should where days are kept
  --metrics-file <path>    write the run as Prometheus text
  --evidence-dir <path>    on a failed check, keep a screenshot of the page here. Off unless given.
                           A screenshot shows the live map: never a directory that is committed or uploaded
  --evidence-pages         with it, also keep the page's HTML and the text of what went wrong. More of the
                           map than a picture; never the session cookie, and no request or response
  --only a,b               run only these checks
  durations and budgets: --stream-seconds --stream-gap-seconds --follow-mob-seconds --follow-player-seconds
                         --follow-tolerance-px --api-budget-ms --evidence-keep --evidence-days --evidence-max-mb
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
  // An address of any other kind is a file or a command to something.
  if (get('internal-url') !== undefined && !/^https?:\/\/[^/\s]+\/?$/.test(String(get('internal-url')))) throw new Error('--internal-url must be the http or https address of the map\'s internal listener, with no path');
  // The secret goes to this address in a header. In the clear is how the
  // internal listener is spoken to inside the cluster, by everything that
  // speaks to it, and over a port-forward to this machine; to anywhere
  // else it is not sent unless the connection is encrypted.
  if (get('internal-url') !== undefined) {
    const internal = new URL(String(get('internal-url')));
    const near = ['127.0.0.1', 'localhost', '[::1]'].includes(internal.hostname) || !internal.hostname.includes('.') || /\.svc(\.cluster\.local)?$/.test(internal.hostname);
    if (internal.protocol !== 'https:' && !near) throw new Error('--internal-url may be plain http only for this machine or a Service inside the cluster; anywhere else it must be https');
  }
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
    keepsHistory: !(values['no-history'] === true || process.env.MCMAP_E2E_NO_HISTORY === 'true'),
    mode: local ? 'local' : external ? 'external' : 'cluster',
    metricsFile: get('metrics-file'),
    evidenceDir: get('evidence-dir'),
    evidencePages: values['evidence-pages'] === true || process.env.MCMAP_E2E_EVIDENCE_PAGES === 'true',
    evidenceMaxBytes: number('evidence-max-mb', 1, 200) * 1024 * 1024,
    evidenceKeep: number('evidence-keep', 1, 50),
    evidenceDays: number('evidence-days', 1, 30),
    streamSeconds: number('stream-seconds', 10, 280),
    streamGapSeconds: number('stream-gap-seconds', 3, 25),
    followMobSeconds: number('follow-mob-seconds', 5, 180),
    followPlayerSeconds: number('follow-player-seconds', 5, 180),
    followTolerancePx: number('follow-tolerance-px', 1, 50),
    apiBudgetMs: number('api-budget-ms', 100, 30_000),
    faceDensities: DENSITIES,
    only: get('only') ? String(get('only')).split(',').map((name) => name.trim()).filter(Boolean) : null,
  };
  // A name that is no check's would run nothing and report a clean run.
  for (const name of cfg.only || []) if (!CHECKS.includes(name)) throw new Error(`--only names ${JSON.stringify(name)}, which is no check; the checks are ${CHECKS.join(', ')}`);
  if (cfg.insecureLocalTls && !local) throw new Error('--insecure-local-tls is for the harness on this machine and nothing else');
  if (Boolean(cfg.secretFile) === Boolean(cfg.secretRef)) throw new Error('give the secret one way: --secret-file, or --secret-from-kube');
  if (Boolean(cfg.internalUrl) === Boolean(cfg.forward)) throw new Error('give the internal listener one way: --internal-url, or --port-forward');
  // A run from outside the cluster against a live map is the owner's to
  // allow, each run. Who allowed it and when is asked for and sent with
  // the exchange, where the map writes it on its audit line. It is a
  // record and not a lock: nothing here can check it.
  if (external && !/^[A-Za-z0-9][A-Za-z0-9._@:+-]{2,63}$/.test(cfg.approvedBy || '')) throw new Error('a run from outside the cluster needs --approved-by <who allowed this run and when>, as one word, such as owner@2026-01-01T00:00Z');
  if (cfg.evidencePages && !cfg.evidenceDir) throw new Error('--evidence-pages needs --evidence-dir');
  // Said in every request, so that the map's logs and the gateway's can
  // tell a check from a visitor.
  cfg.agent = `mcmap-e2e/${VERSION} (service:e2e; ${cfg.mode}${external ? `; approved-by ${cfg.approvedBy}` : ''})`;
  return cfg;
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
  const evidence = new Evidence(cfg.evidenceDir ? path.join(cfg.evidenceDir, `run-${new Date().toISOString().replace(/[-:]|\.\d+/g, '')}`) : null, { pages: cfg.evidencePages, maxBytes: cfg.evidenceMaxBytes });
  // Before anything else, so that a run killed at any point after this has
  // already said that it began and has not finished. Whatever the last run
  // left standing is replaced here, not at the end.
  await report.writeMetrics(cfg.metricsFile);

  let browser;
  let forwarded;
  let broke = false;
  // The verdict is given once, whatever ended the run: its last check, a
  // fault, or a signal from whatever is killing it.
  // Whoever asks second waits for the first: a signal that arrives while
  // the verdict is being written must not end the process under it.
  let closing = null;
  const close = () => {
    closing ??= (async () => {
      report.finish();
      await report.writeMetrics(cfg.metricsFile);
      report.summary({ evidence_files: evidence.files, evidence_errors: evidence.errors });
    })();
    return closing;
  };
  for (const signal of ['SIGTERM', 'SIGINT']) {
    process.once(signal, async () => {
      broke = true;
      report.record('run', FAIL, Date.now() - report.started, 'the run was stopped before it finished');
      await close();
      if (browser) await browser.close().catch(() => {});
      process.exit(2);
    });
  }

  // A fault nothing caught: a promise left unanswered, an error thrown
  // where no check was waiting. It ends the run with a verdict like any
  // other, through the same reporter, and none of what it said is written:
  // an error's text can hold an address, and an address holds a place.
  let failing = false;
  const fatal = async () => {
    if (failing) return;
    failing = true;
    broke = true;
    report.record('run', FAIL, Date.now() - report.started, 'the run itself broke');
    await close();
    if (browser) await browser.close().catch(() => {});
    process.exit(2);
  };
  process.on('unhandledRejection', fatal);
  process.on('uncaughtException', fatal);

  // Said only once a signal would be answered: whoever waits for this
  // line may stop the run the moment they read it.
  report.begin(Boolean(cfg.evidenceDir));

  // One check: its verdict, how long it took, and what it measured. The
  // run's metrics are written again before any evidence is, so that
  // evidence which cannot be written costs nothing but itself.
  // t is what every check is handed. closers is whatever the check now
  // running has opened for itself, to be shut if it has to be stopped.
  const t = { cfg, watch, report, closers: [] };
  // on names the shared page a check works on, 'page' or 'watcher'.
  const run = async (name, fn, { on = null, seconds = 120 } = {}) => {
    if (!CHECKS.includes(name)) throw new Error(`${name} is not in the list of checks`);
    if (cfg.only && !cfg.only.includes(name) && !NEEDED.includes(name)) return null;
    const began = Date.now();
    t.closers = [];
    // A check past its time is stopped before the next begins: what it
    // opened is closed, and the page it shared with the others is closed
    // under it and opened again for them.
    const stop = async () => {
      await Promise.allSettled(t.closers.map((shut) => shut()));
      if (!on || !t[on]) return;
      await t[on].close().catch(() => {});
      t[on] = on === 'page' ? await openMap(t.ctx, cfg).catch(() => null) : null;
    };
    let result;
    let failure = null;
    try {
      const got = await within(fn, seconds, stop) || {};
      result = got.skip ? report.record(name, SKIP, Date.now() - began, got.skip, got.measures) : report.record(name, PASS, Date.now() - began, '', got.measures);
    } catch (err) {
      failure = err;
      const known = err instanceof Fail || err instanceof ExchangeError;
      result = report.record(name, FAIL, Date.now() - began, known ? (err.reason || err.message) : 'the check itself broke', err instanceof Fail ? err.measures : (err.status ? { status: err.status } : {}));
    }
    await report.writeMetrics(cfg.metricsFile);
    if (failure) await evidence.of(name, on ? t[on] : null, failure);
    return result;
  };

  try {
    if (cfg.evidenceDir) await prune(cfg.evidenceDir, cfg.evidenceKeep, cfg.evidenceDays).catch(() => { evidence.errors += 1; });
    // A container gives /dev/shm 64 MB, which a page of canvases outgrows;
    // the browser is told to use its temporary directory instead.
    browser = await chromium.launch({ executablePath: process.env.MCMAP_E2E_CHROMIUM || undefined, args: ['--disable-dev-shm-usage'],
      // A signal is this runner's to answer, with its verdict; left to the
      // library the process would end before it had given one.
      handleSIGINT: false, handleSIGTERM: false, handleSIGHUP: false });
    // The browser's own, with who this is on the end of it.
    const blank = await browser.newContext();
    const plain = await (await blank.newPage()).evaluate(() => navigator.userAgent);
    await blank.close();
    cfg.userAgent = `${plain} ${cfg.agent}`;

    t.browser = browser;
    await run('login-screen', () => access.loginScreen(t));

    await run('exchange', async () => {
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
      t.ctx = ctx;
      // The watcher is opened first and then left alone: it is the
      // stream that page holds that the last check asks about.
      const loaded = await run('map-loads', async () => {
        const began = Date.now();
        t.watcher = await openMap(ctx, cfg);
        t.page = await openMap(ctx, cfg);
        return { measures: { load_ms: Math.round((Date.now() - began) / 2) } };
      }, { seconds: 90 });
      if (loaded && loaded.status !== PASS) throw new Fail('the map did not load with a session, so nothing on it was checked');
      const on = { on: 'page' };
      // Each takes the page as it is when its turn comes: a check that was
      // stopped leaves the next a new one.
      const needs = (fn) => () => {
        if (!t.page) throw new Fail('the map did not load with a session, so nothing on it was checked');
        return fn(t);
      };
      await run('session', needs(access.session), on);
      await run('scope', needs(access.scope), on);
      await run('headers', needs(access.headers), on);
      await run('scripts', needs(access.scripts), on);
      await run('api-budget', needs(access.apiBudget), on);
      await run('dimensions', needs(map.dimensions), on);
      await run('player-heads', needs(live.playerHeads), on);
      await run('zoom-sizes', needs(map.zoomSizes), on);
      await run('layer-panel', needs(map.layerPanel), on);
      await run('toggles', needs(map.toggles), on);
      await run('search', needs(map.search), on);
      await run('structure-sheet', needs(map.structureSheet), on);
      await run('saved-views', needs(map.savedViews), on);
      await run('themes', needs(map.themes), on);
      await run('follow-player', needs(live.followPlayer), { ...on, seconds: cfg.followPlayerSeconds + 60 });
      await run('follow-mob', needs(live.followMob), { ...on, seconds: cfg.followMobSeconds + 60 });
      await run('faces', () => live.faces(t), { seconds: 180 });
      await run('phone-sheet', () => map.phoneSheet(t));
      await run('live-stream', () => live.liveStream(t), { on: 'watcher', seconds: cfg.streamSeconds + 90 });
      await ctx.close();
    } else {
      report.stopped('no session, so nothing behind the login was checked');
    }

    // Over the whole run, in every page it opened.
    await run('page-errors', async () => {
      const { writes, ...measures } = watch.counts();
      if (measures.page_errors > 0) throw new Fail("the page's scripts threw an error", measures);
      if (measures.failed_responses > 0) throw new Fail('an answer was refused or broken that should not have been', measures);
      if (measures.failed_requests > 0) throw new Fail('a request never got an answer', measures);
      return { measures };
    });
    await run('read-only', async () => {
      if (watch.writes.length > 0) throw new Fail('the page tried to write, and was stopped', { writes: watch.writes.length });
      return { measures: { writes: 0 } };
    });
  } catch (err) {
    if (err instanceof Fail) report.stopped(err.message);
    else {
      broke = true;
      report.record('run', FAIL, Date.now() - report.started, 'the run itself broke');
      await evidence.of('run', null, err);
    }
  } finally {
    if (forwarded) forwarded.close();
    if (browser) await browser.close().catch(() => {});
    if (evidence.pages && report.failed > 0) await evidence.put('seen.json', JSON.stringify(watch.raw, null, 1));
    await close();
  }
  return broke ? 2 : report.failed > 0 ? 1 : 0;
}

process.exitCode = await main();
