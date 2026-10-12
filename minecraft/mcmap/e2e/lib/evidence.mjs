// What a failed check may leave behind, and for how long.

import { mkdir, readdir, rm, stat, writeFile } from 'node:fs/promises';
import path from 'node:path';
import { holds, redact } from './session.mjs';

// Evidence shows the live map, so only so much of it is kept and only for
// so long: the newest few failed runs, and none older than the limit.
export async function prune(dir, keep, days) {
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

// What a failed check leaves behind, when it is asked to leave anything.
// Every write is bounded and none can fail the run: a full volume costs
// the evidence and nothing else. Text is written with the secret and the
// session taken out, and is not written at all if either is still in it.
export class Evidence {
  constructor(dir, { pages, maxBytes }) {
    this.dir = dir;
    this.pages = pages;
    this.left = maxBytes;
    this.files = 0;
    this.errors = 0;
  }

  async put(name, data) {
    if (!this.dir) return;
    try {
      const bytes = Buffer.isBuffer(data) ? data : Buffer.from(redact(data), 'utf8');
      if (bytes.length > this.left || (!Buffer.isBuffer(data) && holds(bytes.toString('utf8')))) { this.errors += 1; return; }
      await mkdir(this.dir, { recursive: true, mode: 0o700 });
      await writeFile(path.join(this.dir, name), bytes, { mode: 0o600 });
      this.left -= bytes.length;
      this.files += 1;
    } catch {
      this.errors += 1;
    }
  }

  async of(check, page, error) {
    if (!this.dir) return;
    if (page && !page.isClosed()) {
      const shot = await page.screenshot({ timeout: 10_000 }).catch(() => null);
      if (shot) await this.put(`${check}.png`, shot);
      else this.errors += 1;
      if (this.pages) await this.put(`${check}.html`, await page.content().catch(() => ''));
    }
    if (this.pages) await this.put(`${check}.txt`, String(error.stack || error));
  }
}
