import test from 'node:test';
import assert from 'node:assert/strict';
import { explain, CODES } from './signin-messages.js';

test('every server error code has a message and a next step', () => {
  for (const code of CODES) {
    const m = explain({ refused: true, status: 400, code }, 'login');
    assert.ok(m.text && m.next, code);
  }
});

test('a refused login points at recovery, never at a generic status', () => {
  const m = explain({ refused: true, status: 401, code: 'login_refused' }, 'login');
  assert.match(m.next, /invite/);
});

test('an unknown code falls back to the status, then to a generic message', () => {
  assert.match(explain({ refused: true, status: 404 }, 'login').text, /not turned on/);
  assert.match(explain({ refused: true, status: 429 }, 'login').next, /minute/);
  assert.equal(explain({ refused: true, status: 418, code: 'x' }, 'login').text, 'Sign-in failed.');
});

test('a browser rejection is explained by its name and the kind of ceremony', () => {
  const dom = name => ({ name });
  assert.match(explain(dom('NotAllowedError'), 'login').next, /no passkey for this console/);
  assert.match(explain(dom('NotAllowedError'), 'register').text, /not created/);
  assert.match(explain(dom('InvalidStateError'), 'register').text, /already has a passkey/);
  assert.match(explain(dom('SecurityError'), 'login').text, /address/);
  assert.equal(explain(dom('WeirdError'), 'login').text, 'Sign-in failed.');
});

test('an unreachable server is a connection problem', () => {
  assert.match(explain({ network: true }, 'login').next, /connection/);
});
