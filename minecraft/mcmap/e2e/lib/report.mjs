// What a run says of itself. Logs and metrics leave the cluster for
// dashboards, pagers and pull requests, and the map they describe shows
// where people live. So a result is a name, a verdict, a time and numbers,
// and every piece of text passes through scrub on its way out.

import { mkdir, rename, writeFile } from 'node:fs/promises';
import path from 'node:path';
import { redact } from './session.mjs';

export const PASS = 'pass';
export const FAIL = 'fail';
export const SKIP = 'skip';

// The only fields of a log line that are words. Each is written by this
// suite, in its own words: scrub can take a coordinate out of a sentence
// and cannot know a name from any other word, so nothing read off the page
// is ever put in one.
const WORDS = new Set(['check', 'status', 'detail', 'why', 'mode']);

// Text made safe to leave: no address, no run of digits long enough to be
// a coordinate or an id, nothing quoted, and no letters but plain ones.
// What a check has to say is said in words and in the numbers of its
// measures, which are named here and never taken from the page.
export function scrub(text) {
  return redact(String(text))
    .replace(/\b[a-z][a-z0-9+.-]*:\/\/\S+/gi, '[address]')
    .replace(/(["'`]).*?\1/g, '[quoted]')
    .replace(/#\S+/g, '[fragment]')
    // A line of one of the page's scripts is kept: it says where a fault
    // is and nothing of the world.
    .replace(/(\.js:\d+)|-?\d+(\.\d+)?/g, (n, line) => (line || n.replace(/\D/g, '').length < 2 ? n : 'N'))
    .replace(/[^\x20-\x7e]/g, '?')
    .slice(0, 300);
}

const LABEL = /^[a-z0-9_/.-]{1,64}$/;

export class Report {
  constructor({ mode, version, now = () => Date.now(), write = (line) => process.stdout.write(`${line}\n`) }) {
    this.mode = mode;
    this.version = version;
    this.now = now;
    this.write = write;
    this.started = now();
    this.results = [];
    this.gauges = [];
  }

  // One line of the log, as JSON. Strings in it are scrubbed, numbers and
  // verdicts go as they are, and anything else is left out.
  log(event, fields = {}) {
    const line = { time: new Date(this.now()).toISOString(), event };
    for (const [key, value] of Object.entries(fields)) {
      if (typeof value === 'number' && Number.isFinite(value)) line[key] = value;
      else if (typeof value === 'boolean') line[key] = value;
      else if (key === 'version' && /^[0-9a-z.+-]{1,40}$/i.test(value)) line[key] = value;
      else if (typeof value === 'string' && WORDS.has(key)) line[key] = scrub(value);
      else if (key === 'measures' && value && typeof value === 'object') {
        line[key] = {};
        for (const [name, n] of Object.entries(value)) if (typeof n === 'number' && Number.isFinite(n) && LABEL.test(name)) line[key][name] = Math.round(n * 1000) / 1000;
      }
    }
    this.write(JSON.stringify(line));
  }

  record(check, status, ms, detail = '', measures = {}) {
    const result = { check, status, ms: Math.round(ms), detail: scrub(detail), measures: {} };
    for (const [name, n] of Object.entries(measures)) if (typeof n === 'number' && Number.isFinite(n) && LABEL.test(name)) result.measures[name] = n;
    this.results.push(result);
    this.log('check', { check, status, ms: result.ms, detail: result.detail, measures: result.measures });
    return result;
  }

  // A measurement worth a series of its own: a name, numbers by label.
  gauge(name, help, label, values) {
    const kept = Object.entries(values).filter(([key, n]) => LABEL.test(key) && typeof n === 'number' && Number.isFinite(n));
    if (kept.length > 0) this.gauges.push({ name, help, label, values: kept });
  }

  get failed() {
    return this.results.filter((r) => r.status === FAIL).length;
  }

  // The run as Prometheus reads it. Written whole, to a file beside the
  // one it replaces, so that a scrape never finds half of it.
  metrics() {
    const lines = [];
    const metric = (name, help, rows) => {
      lines.push(`# HELP ${name} ${help}`, `# TYPE ${name} gauge`);
      for (const [labels, value] of rows) lines.push(`${name}${labels} ${value}`);
    };
    const count = (status) => this.results.filter((r) => r.status === status).length;
    const at = this.now();
    metric('mcmap_e2e_run_timestamp_seconds', 'When the last run of the browser checks finished.', [['', Math.floor(at / 1000)]]);
    metric('mcmap_e2e_run_success', '1 if no check of the last run failed.', [['', this.failed === 0 ? 1 : 0]]);
    metric('mcmap_e2e_run_duration_seconds', 'How long the last run took.', [['', ((at - this.started) / 1000).toFixed(1)]]);
    metric('mcmap_e2e_run_info', 'The suite that ran, and from where: cluster, external or local.', [[`{version="${this.version}",mode="${this.mode}"}`, 1]]);
    metric('mcmap_e2e_checks', 'Checks of the last run by how they ended. A skipped check had nothing in the world to look at.', [PASS, FAIL, SKIP].map((s) => [`{status="${s}"}`, count(s)]));
    metric('mcmap_e2e_check_status', 'Each check of the last run: 1 passed, 0 failed, -1 skipped.', this.results.map((r) => [`{check="${r.check}"}`, r.status === PASS ? 1 : r.status === FAIL ? 0 : -1]));
    metric('mcmap_e2e_check_duration_seconds', 'How long each check took.', this.results.map((r) => [`{check="${r.check}"}`, (r.ms / 1000).toFixed(3)]));
    const measured = this.results.flatMap((r) => Object.entries(r.measures).map(([name, n]) => [`{check="${r.check}",measure="${name}"}`, n]));
    metric('mcmap_e2e_measure', 'What each check measured, by the name it gives it.', measured);
    for (const g of this.gauges) metric(g.name, g.help, g.values.map(([key, n]) => [`{${g.label}="${key}"}`, n]));
    return `${lines.join('\n')}\n`;
  }

  async writeMetrics(file) {
    await mkdir(path.dirname(file), { recursive: true });
    const partial = `${file}.partial`;
    await writeFile(partial, this.metrics(), { mode: 0o644 });
    await rename(partial, file);
  }

  summary() {
    const count = (status) => this.results.filter((r) => r.status === status).length;
    this.log('run', { passed: count(PASS), failed: count(FAIL), skipped: count(SKIP), seconds: (this.now() - this.started) / 1000, success: this.failed === 0 });
  }
}
