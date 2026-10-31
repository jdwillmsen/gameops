// Adds each recorded video to the scenario it belongs to in a run's report.json,
// as a relative path under that scenario's `artifacts`. The video's file name is
// the scenario id (videos/<scenario id>.mp4), which is how the recorder is asked
// to name a clip per scenario.
//
// usage: node attach-videos.mjs <bundle dir>
import { existsSync, readFileSync, writeFileSync } from 'node:fs';
import { join } from 'node:path';

export function attach(report, existsFn) {
  const attached = [];
  const missing = [];
  for (const s of report.scenarios ?? []) {
    const rel = `videos/${s.id}.mp4`;
    s.artifacts ??= [];
    if (!existsFn(rel)) { missing.push(s.id); continue; }
    if (!s.artifacts.includes(rel)) s.artifacts.push(rel);
    for (const extra of [`videos/${s.id}.srt`, `videos/${s.id}.json`]) {
      if (existsFn(extra) && !s.artifacts.includes(extra)) s.artifacts.push(extra);
    }
    attached.push(s.id);
  }
  return { attached, missing };
}

if (import.meta.url === `file://${process.argv[1]}`) {
  const dir = process.argv[2];
  if (!dir) { console.error('usage: node attach-videos.mjs <bundle dir>'); process.exit(2); }
  const file = join(dir, 'report.json');
  if (!existsSync(file)) { console.error(JSON.stringify({ error: `no report.json in ${dir}` })); process.exit(1); }
  const report = JSON.parse(readFileSync(file, 'utf8'));
  const result = attach(report, rel => existsSync(join(dir, rel)));
  writeFileSync(file, JSON.stringify(report, null, 2) + '\n');
  console.log(JSON.stringify(result));
}
