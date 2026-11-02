import test from 'node:test';
import assert from 'node:assert/strict';
import { readdirSync, readFileSync } from 'node:fs';
import { join, dirname } from 'node:path';
import { fileURLToPath } from 'node:url';

const root = dirname(fileURLToPath(import.meta.url));

function files(dir, ext, skip = () => false) {
  return readdirSync(join(root, dir)).filter(f => f.endsWith(ext) && !skip(f)).map(f => join(dir, f));
}
const read = f => readFileSync(join(root, f), 'utf8');

// SVG builders are separate helpers and not covered here; this is the HTML builder.
test('no view defines its own element builder; they use kit/dom.js', () => {
  const views = [...files('.', '.js'), ...files('graph', '.js', f => f.endsWith('fake-dom.mjs'))];
  const own = /\bfunction el\(|\b(?:const|let) el\s*=/;
  assert.deepEqual(views.filter(f => own.test(read(f))), []);
});

// These values are the :root tokens in console.css, which themes override; a
// literal copy in a view stylesheet would ignore the theme.
test('view stylesheets use the colour tokens instead of repeating their values', () => {
  const sheets = [...files('.', '.css', f => f === 'console.css'), 'graph/graph.css', 'graph/ledger.css', ...files('kit', '.css')];
  const literal = /#(?:111|1b1b1b|ddd|333|6fd08c|f0b040|ff5d5d|5fd0ff|2a2f2c)\b(?![0-9a-f])/i;
  assert.deepEqual(sheets.filter(f => literal.test(read(f))), []);
});

// confirmDialog returns a Promise, which is always truthy: a call site that
// forgets the await would act on every answer, Cancel included.
test('every confirmDialog call site awaits the answer', () => {
  const calls = [...files('.', '.js')].flatMap(f => read(f).split('\n').filter(l => /confirmDialog\(/.test(l) && !/import /.test(l)).map(l => [f, l]));
  assert.ok(calls.length >= 2);
  assert.deepEqual(calls.filter(([, l]) => !/await confirmDialog\(/.test(l)), []);
});
