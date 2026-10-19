import test from 'node:test';
import assert from 'node:assert/strict';
import { parse, format, MODES, tileSrc, wallSrc, badge } from './state.js';

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
