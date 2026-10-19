import test from 'node:test';
import assert from 'node:assert/strict';
import { parse, format, MODES, tileSrc, wallSrc, badge, readPoint, gotoBody, taskBody, startPoint, splitWork, describeItem, isTyping } from './state.js';

const units = [{ id: 'crew-01' }, { id: 'crew-02' }, { id: 'crew-03' }];

test('parse(format(s)) round-trips every unit, mode and view', () => {
  for (const u of units) for (const mode of MODES) for (const view of ['focus', 'wall']) {
    const s = { unit: u.id, mode, view };
    assert.deepEqual(parse(format(s), units), s);
  }
});

test('empty search gives first unit, first mode, focus view', () => {
  assert.deepEqual(parse('', units), { unit: 'crew-01', mode: 'first', view: 'focus' });
});

test('unknown values fall back', () => {
  assert.deepEqual(parse('?unit=nope&mode=fly&view=grid', units), { unit: 'crew-01', mode: 'first', view: 'focus' });
});

test('known values survive next to unknown ones', () => {
  assert.deepEqual(parse('?unit=crew-03&mode=bogus', units), { unit: 'crew-03', mode: 'first', view: 'focus' });
});

test('parse tolerates an empty roster', () => {
  assert.equal(parse('?unit=x', []).unit, '');
});

test('format encodes ids', () => {
  assert.ok(!format({ unit: 'a&b', mode: 'first', view: 'focus' }).includes('a&b'));
});

test('tile urls', () => {
  assert.equal(tileSrc('crew-02', 'orbit'), '/units/crew-02/camera?mode=orbit');
  assert.equal(wallSrc('crew-02'), '/units/crew-02/camera?mode=first&hud=0&r=2');
});

test('badge text', () => {
  assert.equal(badge({ online: false }), 'offline');
  assert.equal(badge({ online: true, dead: true, health: 0 }), 'dead');
  assert.equal(badge({ online: true, health: 14, task: null }), 'hp 14 idle');
  assert.equal(badge({ online: true, health: null, task: { template: 'mine', state: 'running', item: 'oak_log', qty: 8, progress: 3 } }), 'mine running');
});

const p = (x, y, z) => ({ x: String(x), y: String(y), z: String(z) });

test('readPoint accepts the corners of the allowed area', () => {
  assert.deepEqual(readPoint(p(-500, -64, -500)).point, { x: -500, y: -64, z: -500 });
  assert.deepEqual(readPoint(p(499, 320, 499)).point, { x: 499, y: 320, z: 499 });
});

test('readPoint refuses out-of-range, blank and non-integer values', () => {
  for (const raw of [p(500, 0, 0), p(-501, 0, 0), p(0, 321, 0), p(0, -65, 0), p(0, 0, 500), p(0, 0, -501), p('', 0, 0), p(1.5, 0, 0), p('abc', 0, 0), p('1e2', 0, 0), p(' ', 0, 0)]) {
    assert.ok(readPoint(raw).error, JSON.stringify(raw));
  }
});

test('gotoBody is the bare position', () => {
  assert.deepEqual(gotoBody(p(10, 64, -20)), { body: { x: 10, y: 64, z: -20 } });
  assert.ok(gotoBody(p(10, 999, -20)).error);
});

test('gather body', () => {
  const form = { template: 'gather', item: 'oak_log', qty: '8', chest: p(1, 2, 3) };
  assert.deepEqual(taskBody(form), { body: { template: 'gather_and_deposit', item: 'oak_log', qty: 8, chest: { x: 1, y: 2, z: 3 } } });
});

test('qty is 1 to 256', () => {
  const at = qty => taskBody({ template: 'gather', item: 'cobblestone', qty, chest: p(0, 0, 0) });
  assert.ok(at('1').body && at('256').body);
  for (const bad of ['0', '257', '', '2.5', '-1', 'x']) assert.ok(at(bad).error, bad);
});

test('gather refuses an unknown item', () => {
  assert.ok(taskBody({ template: 'gather', item: 'diamond', qty: '1', chest: p(0, 0, 0) }).error);
});

test('haul body', () => {
  const form = { template: 'haul', item: 'cobblestone', qty: '64', from: p(1, 2, 3), chest: p(4, 5, 6) };
  assert.deepEqual(taskBody(form), { body: { template: 'haul', items: { cobblestone: 64 }, from: [{ x: 1, y: 2, z: 3 }], to: { x: 4, y: 5, z: 6 } } });
});

test('haul needs both chests in bounds', () => {
  assert.ok(taskBody({ template: 'haul', item: 'cobblestone', qty: '1', from: p(1, 2, 3), chest: p(900, 5, 6) }).error);
  assert.ok(taskBody({ template: 'haul', item: 'cobblestone', qty: '1', from: p('', 2, 3), chest: p(4, 5, 6) }).error);
});

test('unknown template is refused', () => {
  assert.ok(taskBody({ template: 'mine', item: 'oak_log', qty: '1', chest: p(0, 0, 0) }).error);
});

test('startPoint reads a known position and rounds down to a block', () => {
  assert.deepEqual(startPoint({ state: { position: [10.7, 64.62, -3.2], position_known: true } }), { x: 10, y: 64, z: -4 });
  assert.equal(startPoint({ state: { position: [1, 2, 3], position_known: false } }), null);
  assert.equal(startPoint({}), null);
  assert.equal(startPoint(null), null);
});

test('splitWork keeps open items apart from history', () => {
  const items = [{ id: 'a', state: 'running' }, { id: 'b', state: 'done' }, { id: 'c', state: 'queued' }, { id: 'd', state: 'delivered' }, { id: 'e', state: 'cancelled' }, { id: 'f', state: 'failed' }];
  const { open, history } = splitWork(items);
  assert.deepEqual(open.map(i => i.id), ['a', 'c', 'd']);
  assert.deepEqual(history.map(i => i.id), ['b', 'e', 'f']);
});

test('describeItem', () => {
  assert.equal(describeItem({ kind: 'task', body: { template: 'gather_and_deposit', item: 'oak_log', qty: 8 } }), 'gather oak_log x8');
  assert.equal(describeItem({ kind: 'task', body: { template: 'haul', items: { cobblestone: 64 } } }), 'haul cobblestone x64');
  assert.equal(describeItem({ kind: 'task', body: { template: 'goto', to: { x: 1, y: 2, z: 3 } } }), 'go to 1 2 3');
  assert.equal(describeItem({ kind: 'command', body: { name: 'stop' } }), 'stop');
  assert.equal(describeItem({ kind: 'task', body: null }), 'task');
});

test('typing targets do not trigger shortcuts', () => {
  for (const tag of ['INPUT', 'SELECT', 'TEXTAREA']) assert.equal(isTyping({ tagName: tag }), true);
  assert.equal(isTyping({ tagName: 'BUTTON' }), false);
  assert.equal(isTyping({ tagName: 'DIV', isContentEditable: true }), true);
  assert.equal(isTyping(null), false);
});
