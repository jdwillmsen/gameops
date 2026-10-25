import test from 'node:test';
import assert from 'node:assert/strict';
import { parse, format, MODES, tileSrc, wallSrc, badge, readPoint, gotoBody, taskBody, startPoint, splitWork, describeItem, isTyping, canCancel, signinParams, stockBody, seenAgo, stepCount, placeBody, renameBody, jobTitle, knownState } from './state.js';

const units = [{ id: 'crew-01' }, { id: 'crew-02' }, { id: 'crew-03' }];

test('parse(format(s)) round-trips every unit, mode and view', () => {
  for (const u of units) for (const mode of MODES) for (const view of ['focus', 'wall', 'fleet', 'jobs']) {
    const s = { unit: u.id, mode, view };
    assert.deepEqual(parse(format(s), units), { ...s, job: '' });
  }
});

test('fmt keeps a linked job as its own parameter', () => {
  const link = { unit: 'crew-03', mode: 'first', view: 'jobs', job: '03040506-0010-2020-3030-405060708090' };
  // The round trip carries the deep link; format omits it unset.
  assert.deepEqual(parse(format(link), units), { ...link });
  assert.deepEqual(parse(format({ unit: 'crew-03', mode: 'first', view: 'jobs' }), units).job, '');
});

test('empty search gives first unit, first mode, focus view', () => {
  assert.deepEqual(parse('', units), { unit: 'crew-01', mode: 'first', view: 'focus', job: '' });
});

test('unknown values fall back', () => {
  assert.deepEqual(parse('?unit=nope&mode=fly&view=grid', units), { unit: 'crew-01', mode: 'first', view: 'focus', job: '' });
});

test('known values survive next to unknown ones', () => {
  assert.deepEqual(parse('?unit=crew-03&mode=bogus', units), { unit: 'crew-03', mode: 'first', view: 'focus', job: '' });
});

test('a job parameter survives only when it reads like a uuid', () => {
  // A typo, a partial id or junk is dropped rather than fetched along a deep link.
  for (const junk of ['hello!', '03040506-0010-2020-3030', '--------']) {
    assert.deepEqual(parse(`?view=jobs&job=${junk}`, units), { unit: 'crew-01', mode: 'first', view: 'jobs', job: '' });
  }
  assert.equal(parse('?view=jobs&job=03040506-0010-2020-3030-405060708090', units).job, '03040506-0010-2020-3030-405060708090');
  assert.equal(parse('?view=jobs&job=03040506-0010-2020-3030-40506070809A', units).job, '03040506-0010-2020-3030-40506070809A');
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

test('a stop command is never offered a Cancel button', () => {
  assert.equal(canCancel({ kind: 'command', state: 'delivered', body: { name: 'stop' } }), false);
  assert.equal(canCancel({ kind: 'task', state: 'queued' }), true);
});

test('sign-in reads operator and invite from the fragment', () => {
  assert.deepEqual(signinParams('#operator=owner&invite=abc-123'), { invite: 'abc-123', operator: 'owner' });
  assert.deepEqual(signinParams(''), { invite: '', operator: '' });
  assert.deepEqual(signinParams('#invite=a%20b'), { invite: 'a b', operator: '' });
});

test('stockBody builds the job request and rejects bad rows', () => {
  assert.deepEqual(stockBody('store', [{ item: 'oak_log', qty: '32' }, { item: 'cobblestone', qty: 32 }]),
    { body: { kind: 'stock', place: 'store', want: { oak_log: 32, cobblestone: 32 } } });
  assert.match(stockBody('', [{ item: 'oak_log', qty: '1' }]).error, /chest/);
  assert.match(stockBody('s', []).error, /add an item/);
  assert.match(stockBody('s', [{ item: 'dirt', qty: '1' }]).error, /unknown item/);
  assert.match(stockBody('s', [{ item: 'oak_log', qty: '0' }]).error, /qty/);
  assert.match(stockBody('s', [{ item: 'oak_log', qty: '257' }]).error, /qty/);
  assert.match(stockBody('s', [{ item: 'oak_log', qty: '1' }, { item: 'oak_log', qty: '2' }]).error, /twice/);
});

test('seenAgo reads as relative time', () => {
  const now = Date.parse('2026-10-20T12:00:00Z');
  const ago = s => seenAgo(new Date(now - s * 1000).toISOString(), now);
  assert.equal(ago(2), 'seen just now');
  assert.equal(ago(30), 'seen 30s ago');
  assert.equal(ago(180), 'seen 3m ago');
  assert.equal(ago(7300), 'seen 2h ago');
  assert.equal(ago(200000), 'seen 2d ago');
  assert.equal(seenAgo('nope', now), '');
  assert.equal(seenAgo(new Date(now + 5000).toISOString(), now), 'seen just now');
});

test('stepCount and jobTitle summarise a job', () => {
  assert.deepEqual(stepCount({ steps: [{ state: 'done' }, { state: 'running' }] }), { done: 1, total: 2 });
  assert.deepEqual(stepCount({}), { done: 0, total: 0 });
  assert.equal(jobTitle({ kind: 'plan', spec: { template: 'parallel-demo' } }), 'parallel-demo');
  assert.equal(jobTitle({ kind: 'stock', spec: { place: 'store', want: { oak_log: 32 } } }), 'stock store: oak_log x32');
});

test('placeBody and renameBody', () => {
  const pt = { x: '1', y: '64', z: '-2' };
  assert.deepEqual(placeBody({ id: ' c1 ', name: ' Box ', kind: 'chest', min: pt }).body,
    { id: 'c1', name: 'Box', kind: 'chest', box: { min: { x: 1, y: 64, z: -2 }, max: { x: 1, y: 64, z: -2 } } });
  assert.deepEqual(placeBody({ id: 'a', name: 'A', kind: 'area', min: pt, max: { x: '5', y: '66', z: '3' } }).body.box.max, { x: 5, y: 66, z: 3 });
  assert.match(placeBody({ id: '', name: 'n', kind: 'chest', min: pt }).error, /id/);
  assert.match(placeBody({ id: 'a', name: '', kind: 'chest', min: pt }).error, /name/);
  assert.match(placeBody({ id: 'a', name: 'n', kind: 'chest', min: { x: 'q', y: '1', z: '1' } }).error, /whole/);
  assert.match(placeBody({ id: 'a', name: 'n', kind: 'room', min: pt }).error, /kind/);
  const p = { id: 'a', name: 'A', kind: 'area', box: { min: { x: 0, y: 0, z: 0 }, max: { x: 1, y: 1, z: 1 } }, items: ['oak_log'], seeded: false, stock: [] };
  assert.deepEqual(renameBody(p, ' B '), { id: 'a', name: 'B', kind: 'area', box: p.box, items: ['oak_log'] });
});

test('knownState passes only known states', () => {
  assert.equal(knownState('running'), 'running');
  assert.equal(knownState('x y'), 'unknown');
  assert.equal(knownState(undefined), 'unknown');
});
