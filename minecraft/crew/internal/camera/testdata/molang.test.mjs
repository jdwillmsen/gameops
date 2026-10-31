// Pins the Molang subset the pack animations use. Run by TestMolangModule with `node --test`.
import { test } from 'node:test';
import assert from 'node:assert/strict';
import { pathToFileURL } from 'node:url';

const m = await import(pathToFileURL(process.env.MOLANG_JS));
const ev = (src, query = {}, variable = {}) => m.compile(src)?.({ query, variable });

test('arithmetic follows precedence and trigonometry is in degrees', () => {
  assert.equal(ev('1 + 2 * 3'), 7);
  assert.equal(ev('(1 + 2) * 3'), 9);
  assert.equal(ev('-2 * -3'), 6);
  assert.ok(Math.abs(ev('math.sin(90)') - 1) < 1e-9);
  assert.ok(Math.abs(ev('math.cos(180)') + 1) < 1e-9);
  assert.equal(ev('math.clamp(50.0, -30.0, 30.0)'), 30);
});

test('comparisons, logic and the ternary give 1 or 0', () => {
  assert.equal(ev('variable.attack_time > 0', {}, { attack_time: 0.2 }), 1);
  assert.equal(ev('variable.attack_time > 0', {}, { attack_time: 0 }), 0);
  assert.equal(ev('query.variant == 1 && query.is_sitting', { variant: 1, is_sitting: true }), 1);
  assert.equal(ev('query.variant == 1 || 0', { variant: 0 }), 0);
  assert.equal(ev('query.is_sitting ? 5 : 7', { is_sitting: false }), 7);
  assert.equal(ev('!query.is_sitting', { is_sitting: false }), 1);
});

test('the walk expression from the wildlife pack', () => {
  const src = 'math.cos(query.modified_distance_moved * 24.0) * 40.0 * query.modified_move_speed';
  assert.ok(Math.abs(ev(src, { modified_distance_moved: 0, modified_move_speed: 0.5 }) - 20) < 1e-9);
  assert.equal(ev(src, { modified_distance_moved: 3, modified_move_speed: 0 }), 0);
});

test('unknown queries read 0, bad text is refused', () => {
  assert.equal(ev('query.nothing_like_this + 1'), 1);
  for (const bad of ['', '1 +', 'math.nope(1)', 'foo.bar', '(1', '1 2', 'a;b', 'globalThis', 'x'.repeat(600), '__proto__(1)', 'constructor(1)', 'math.constructor(1)']) {
    assert.equal(m.compile(bad), null, bad);
  }
  assert.equal(m.compile('('.repeat(100) + '1' + ')'.repeat(100)), null);
  assert.equal(ev('1 / 0'), 0);
});

test('animations add up per bone and honour their conditions', () => {
  const runs = m.compileAnimations({
    defs: {
      walk: { bones: { Leg: { r: ['10', '0', '0'] } } },
      sit: { bones: { leg: { r: ['-75', '0', '0'] }, head: { p: ['0', '0', '-3'] } } },
    },
    run: [{ n: 'walk' }, { n: 'sit', w: 'query.is_sitting' }],
  });
  assert.equal(runs.length, 2);
  let p = m.pose(runs, { query: { is_sitting: false }, variable: {} });
  assert.deepEqual(p.get('leg').rot, [10, 0, 0]);
  assert.equal(p.has('head'), false);
  p = m.pose(runs, { query: { is_sitting: true }, variable: {} });
  assert.deepEqual(p.get('leg').rot, [-65, 0, 0]);
  assert.deepEqual(p.get('head').pos, [0, 0, -3]);
});

test('an animation with unparsable text is dropped, the rest stay', () => {
  const runs = m.compileAnimations({
    defs: { a: { bones: { x: { r: ['1', '2', 'bad('] } } }, b: { bones: { y: { r: ['1', '2', '3'] } } } },
    run: [{ n: 'a' }, { n: 'b' }, { n: 'missing' }, { n: 'b', w: 'oops(' }],
  });
  assert.equal(runs.length, 2);
  assert.equal(runs[0].bones.size, 0);
});
