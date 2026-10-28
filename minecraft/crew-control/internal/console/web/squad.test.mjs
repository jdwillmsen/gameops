import test from 'node:test';
import assert from 'node:assert/strict';
import { roleLabel, lastCallout, orderLine } from './squad.js';

test('role badges are the claimed roles', () => {
  assert.equal(roleLabel('vanguard'), 'Vanguard');
  assert.equal(roleLabel('striker'), 'Striker');
  assert.equal(roleLabel('ranger'), 'Ranger');
  assert.equal(roleLabel('mender'), 'Mender');
  assert.equal(roleLabel('builder'), 'Builder');
});

test('an unknown or missing role still renders', () => {
  assert.equal(roleLabel('medic'), 'medic');
  assert.equal(roleLabel(''), 'member');
  assert.equal(roleLabel(undefined), 'member');
});

test('last callout is the newest for that member', () => {
  const calls = [
    { kind: 'need_heal', from: 'crew-19' },
    { kind: 'low_health', from: 'crew-18' },
    { kind: 'boss_phase', from: 'crew-19' },
  ];
  assert.equal(lastCallout(calls, 'crew-19').kind, 'need_heal');
  assert.equal(lastCallout(calls, 'crew-18').kind, 'low_health');
});

test('a member with no callout has none', () => {
  assert.equal(lastCallout([{ kind: 'need_heal', from: 'crew-19' }], 'crew-18'), null);
  assert.equal(lastCallout(undefined, 'crew-18'), null);
});

test('the order line names the kind and target', () => {
  assert.equal(orderLine(null), 'no order');
  assert.equal(orderLine({ kind: 'hold' }), 'hold');
  assert.equal(orderLine({ kind: 'focus', target: 'captain' }), 'focus captain');
});
