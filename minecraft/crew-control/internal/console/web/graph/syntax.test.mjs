// The UI modules are not imported by the unit tests (they need a DOM), so a
// syntax error in one would only show up in the browser. Parse each module the
// way the browser will.
import { test } from 'node:test';
import assert from 'node:assert/strict';
import { execFileSync } from 'node:child_process';
import { readFileSync, readdirSync } from 'node:fs';
import { dirname, join } from 'node:path';
import { fileURLToPath } from 'node:url';

const dir = dirname(fileURLToPath(import.meta.url));
const modules = readdirSync(dir).filter(f => f.endsWith('.js'));

test('every graph module parses as an ES module', () => {
  assert.ok(modules.length >= 4);
  for (const file of modules) {
    assert.doesNotThrow(() => execFileSync(process.execPath, ['--input-type=module', '--check'], {
      input: readFileSync(join(dir, file)), stdio: ['pipe', 'ignore', 'pipe'],
    }), `${file} does not parse`);
  }
});
