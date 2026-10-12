// What a run says of itself. Logs and metrics leave the cluster for
// dashboards, pagers and pull requests, and the map they describe shows
// where people live. So a result has no field for words at all: it is a
// check's name, a verdict, a reason and numbers, each drawn from the closed
// sets in fields.mjs. Text read off the page has nowhere to go.

import { mkdir, rename, writeFile } from 'node:fs/promises';
import path from 'node:path';
import { CHECKS, GAUGES, MEASURES, MODES, REASONS, slug } from './fields.mjs';

export const PASS = 'pass';
export const FAIL = 'fail';
export const SKIP = 'skip';
const VERDICTS = [PASS, FAIL, SKIP];
const UNLISTED = 'unlisted';

// The numbers of a log line, and the three fields that are names from a
// closed set. Anything else handed to log is dropped and counted.
const NUMBERS = new Set(['ms', 'passed', 'failed', 'skipped', 'seconds', 'dropped_fields', 'evidence_files', 'evidence_errors']);
const FLAGS = new Set(['success', 'finished', 'evidence']);
const EVENTS = new Set(['start', 'check', 'gauge', 'run', 'stopped']);

export class Report {
  constructor({ mode, version, now = () => Date.now(), write = (line) => process.stdout.write(`${line}\n`) }) {
    this.mode = MODES.includes(mode) ? mode : UNLISTED;
    this.version = /^[0-9a-z.+-]{1,40}$/i.test(String(version)) ? String(version) : UNLISTED;
    this.now = now;
    this.write = write;
    this.started = now();
    this.finished = null;
    this.results = [];
    this.gauges = new Map();
    // How many times something was offered that is in no closed set. It is
    // above zero only when a check has been written wrongly, and the
    // suite's own run fails on it.
    this.dropped = 0;
  }

  measures(given) {
    const kept = {};
    for (const [name, n] of Object.entries(given || {})) {
      if (MEASURES.has(name) && typeof n === 'number' && Number.isFinite(n)) kept[name] = Math.round(n * 1000) / 1000;
      else this.dropped += 1;
    }
    return kept;
  }

  // The reason as it is written: one of the catalogue's, or nothing.
  reason(sentence) {
    if (!sentence) return '';
    const code = slug(sentence);
    if (REASONS.has(code)) return code;
    this.dropped += 1;
    return UNLISTED;
  }

  // Private: the one way a line is written, reached only through the
  // methods below, each of which decides what goes in it. A check holds a
  // report and cannot hand this anything.
  #log(event, fields = {}) {
    if (!EVENTS.has(event)) { this.dropped += 1; return; }
    const line = { time: new Date(this.now()).toISOString(), event, mode: this.mode, version: this.version };
    for (const [key, value] of Object.entries(fields)) {
      if (NUMBERS.has(key) && typeof value === 'number' && Number.isFinite(value)) line[key] = Math.round(value * 1000) / 1000;
      else if (FLAGS.has(key) && typeof value === 'boolean') line[key] = value;
      else if (key === 'check' && (value === UNLISTED || CHECKS.includes(value))) line.check = value;
      else if (key === 'status' && VERDICTS.includes(value)) line.status = value;
      else if (key === 'reason' && (value === '' || value === UNLISTED || REASONS.has(value))) line.reason = value;
      // Both are made by this class from the closed sets, and by nothing else.
      else if (key === 'measures' || key === 'values') line[key] = value;
      else if (key === 'gauge' && Object.hasOwn(GAUGES, value)) line.gauge = value;
      else this.dropped += 1;
    }
    this.write(JSON.stringify(line));
  }

  // That the run has begun, and whether it will keep evidence.
  begin(evidence) {
    this.#log('start', { evidence: evidence === true });
  }

  // That the rest of the run was not made, and why, as one of the catalogue.
  stopped(sentence) {
    this.#log('stopped', { reason: this.reason(sentence) });
  }

  record(check, status, ms, reason = '', measures = {}) {
    const result = {
      check: CHECKS.includes(check) ? check : UNLISTED, status: VERDICTS.includes(status) ? status : FAIL,
      ms: Math.round(Number.isFinite(ms) ? ms : 0), reason: this.reason(reason), measures: this.measures(measures),
    };
    if (result.check === UNLISTED) this.dropped += 1;
    this.results.push(result);
    this.#log('check', result);
    return result;
  }

  // A family of numbers under one label, whose values the gauge itself
  // decides whether to take.
  gauge(name, values) {
    const kind = GAUGES[name];
    if (!kind) { this.dropped += 1; return; }
    const kept = [];
    for (const [key, n] of Object.entries(values || {})) {
      if (kind.allows(key) && typeof n === 'number' && Number.isFinite(n)) kept.push([key, n]);
      else this.dropped += 1;
    }
    if (kept.length === 0) return;
    this.gauges.set(name, kept);
    // In the log as well: where a run's metrics are not collected, the
    // log is where these numbers are read.
    this.#log('gauge', { gauge: name, values: Object.fromEntries(kept.map(([key, n]) => [key, Math.round(n * 1000) / 1000])) });
  }

  get failed() {
    return this.results.filter((r) => r.status === FAIL).length;
  }

  finish() {
    if (this.finished === null) this.finished = this.now();
  }

  // The run as Prometheus reads it, at whatever point it has reached. One
  // that has started and not finished says so, and is no success.
  metrics() {
    const lines = [];
    const metric = (name, help, rows) => {
      lines.push(`# HELP ${name} ${help}`, `# TYPE ${name} gauge`);
      for (const [labels, value] of rows) lines.push(`${name}${labels} ${value}`);
    };
    const count = (status) => this.results.filter((r) => r.status === status).length;
    const done = this.finished !== null;
    metric('mcmap_e2e_run_started_timestamp_seconds', 'When the last run of the browser checks began.', [['', Math.floor(this.started / 1000)]]);
    metric('mcmap_e2e_run_finished_timestamp_seconds', 'When it finished; 0 while it has not.', [['', done ? Math.floor(this.finished / 1000) : 0]]);
    metric('mcmap_e2e_run_finished', '1 once the last run has given its verdict. 0 is a run still going, or one that was killed.', [['', done ? 1 : 0]]);
    metric('mcmap_e2e_run_success', '1 if the last run finished and no check of it failed.', [['', done && this.failed === 0 ? 1 : 0]]);
    metric('mcmap_e2e_run_failed', '1 if a check of the last run has failed, finished or not.', [['', this.failed > 0 ? 1 : 0]]);
    metric('mcmap_e2e_run_duration_seconds', 'How long the last run took, or has taken so far.', [['', (((done ? this.finished : this.now()) - this.started) / 1000).toFixed(1)]]);
    metric('mcmap_e2e_run_info', 'The suite that ran, and from where: cluster, external or local.', [[`{version="${this.version}",mode="${this.mode}"}`, 1]]);
    metric('mcmap_e2e_checks', 'Checks of the last run by how they ended. A skipped check had nothing in the world to look at.', VERDICTS.map((s) => [`{status="${s}"}`, count(s)]));
    metric('mcmap_e2e_check_status', 'Each check of the last run: 1 passed, 0 failed, -1 skipped.', this.results.map((r) => [`{check="${r.check}"}`, r.status === PASS ? 1 : r.status === FAIL ? 0 : -1]));
    metric('mcmap_e2e_check_reason', 'Why each check that failed or was skipped did, by a name from the suite\'s own list.', this.results.filter((r) => r.reason).map((r) => [`{check="${r.check}",reason="${r.reason}"}`, 1]));
    metric('mcmap_e2e_check_duration_seconds', 'How long each check took.', this.results.map((r) => [`{check="${r.check}"}`, (r.ms / 1000).toFixed(3)]));
    metric('mcmap_e2e_measure', 'What each check measured, by the name it gives it.', this.results.flatMap((r) => Object.entries(r.measures).map(([name, n]) => [`{check="${r.check}",measure="${name}"}`, n])));
    for (const [name, values] of this.gauges) metric(name, GAUGES[name].help, values.map(([key, n]) => [`{${GAUGES[name].label}="${key}"}`, n]));
    metric('mcmap_e2e_dropped_fields', 'Things a check tried to report that are in no list of what may be reported. Above zero is a fault in the suite.', [['', this.dropped]]);
    return `${lines.join('\n')}\n`;
  }

  // Written whole, to a file beside the one it replaces, so that a scrape
  // never finds half of it. Never throws: a volume that is full must not be
  // what stops a run saying how it went, and the log says it either way.
  writeMetrics(file) {
    if (!file) return Promise.resolve(true);
    // One at a time, each writing the run as it stands when its turn
    // comes: two at once would share the file they write beside, and the
    // older could be the one left in place.
    this.writing = (this.writing || Promise.resolve()).then(async () => {
      try {
        await mkdir(path.dirname(file), { recursive: true });
        const partial = `${file}.partial`;
        await writeFile(partial, this.metrics(), { mode: 0o644 });
        await rename(partial, file);
        return true;
      } catch {
        return false;
      }
    });
    return this.writing;
  }

  summary({ evidence_files = 0, evidence_errors = 0 } = {}) {
    const count = (status) => this.results.filter((r) => r.status === status).length;
    this.#log('run', {
      passed: count(PASS), failed: count(FAIL), skipped: count(SKIP), seconds: ((this.finished ?? this.now()) - this.started) / 1000,
      finished: this.finished !== null, success: this.finished !== null && this.failed === 0, dropped_fields: this.dropped, evidence_files, evidence_errors,
    });
  }
}
