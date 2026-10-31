// Runs the wildlife pack's real animations, as the build compiled them, through
// the page's evaluator. Run by TestWildlifeAnimationsEvaluate with `node --test`.
import { test } from 'node:test';
import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import { pathToFileURL } from 'node:url';

const m = await import(pathToFileURL(process.env.MOLANG_JS));
const table = JSON.parse(readFileSync(process.env.PACK_TABLE, 'utf8'));
const SPECIES = ['tiger', 'cheetah', 'lion', 'black_leopard', 'brown_bear', 'hyena'];
const ctx = (query = {}, variable = {}) => ({
  query: { modified_distance_moved: 0, modified_move_speed: 0, life_time: 0, target_x_rotation: 0, target_y_rotation: 0, is_sitting: false, variant: 0, ...query },
  variable: { attack_time: 0, ...variable },
});

for (const name of SPECIES) {
  const model = table.models[`gameops:${name}`];
  test(`${name}: every animation compiles and every pose is finite`, () => {
    assert.ok(model?.an, 'no animations');
    const runs = m.compileAnimations(model.an);
    assert.deepEqual(runs.map(r => r.name), ['walk', 'idle', 'sit', 'attack']);
    for (const c of [ctx(), ctx({ is_sitting: true }), ctx({ modified_distance_moved: 7.3, modified_move_speed: 1 }, { attack_time: 0.5 }), ctx({ target_x_rotation: 90, target_y_rotation: -170 })]) {
      for (const p of m.pose(runs, c).values()) assert.ok([...p.rot, ...p.pos].every(Number.isFinite));
    }
  });
}

test('walking swings the legs in opposite pairs, and standing still does not', () => {
  const runs = m.compileAnimations(table.models['gameops:tiger'].an);
  const walk = m.pose(runs, ctx({ modified_distance_moved: 2, modified_move_speed: 1 }));
  const fl = walk.get('front_left_leg').rot[0], fr = walk.get('front_right_leg').rot[0];
  assert.ok(Math.abs(fl) > 1 && Math.abs(fl + fr) < 1e-9);
  const still = m.pose(runs, ctx({ modified_distance_moved: 2, modified_move_speed: 0 }));
  assert.equal(still.get('front_left_leg').rot[0], 0);
});

test('sitting folds the hind legs and drops the head; standing does not', () => {
  const runs = m.compileAnimations(table.models['gameops:tiger'].an);
  const sit = m.pose(runs, ctx({ is_sitting: true }));
  assert.equal(sit.get('back_left_leg').rot[0], -75);
  assert.equal(sit.get('body').rot[0], -20);
  assert.equal(m.pose(runs, ctx()).get('back_left_leg').rot[0], 0);
});

test('an attack swing lunges the head forward and only while attack_time runs', () => {
  const runs = m.compileAnimations(table.models['gameops:tiger'].an);
  const mid = m.pose(runs, ctx({}, { attack_time: 0.5 })).get('head');
  assert.ok(Math.abs(mid.rot[0] - 30) < 1e-9 && Math.abs(mid.pos[2] + 3) < 1e-9);
  const idle = m.pose(runs, ctx()).get('head');
  assert.deepEqual(idle.pos, [0, 0, 0]);
});

test('the head follows where the entity looks, within its limits', () => {
  const runs = m.compileAnimations(table.models['gameops:tiger'].an);
  const head = m.pose(runs, ctx({ target_x_rotation: 80, target_y_rotation: -170 })).get('head');
  assert.equal(head.rot[0], 30);
  assert.equal(head.rot[1], -60);
});

test('variants: the table carries one layer set per texture, and the lion female has no mane', () => {
  assert.equal(table.models['gameops:tiger'].v.length, 2);
  const cubes = (v, bone) => v[0].bones.find(b => b.n === bone)?.c?.length ?? 0;
  const lion = table.models['gameops:lion'].v;
  assert.ok(cubes(lion[0], 'mane') > 0);
  assert.equal(cubes(lion[1], 'mane'), 0);
  assert.equal(cubes(lion[1], 'mane_chest'), 0);
});
