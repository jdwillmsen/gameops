import test from 'node:test';
import assert from 'node:assert/strict';
import { enrolRefusal, readInviteForm } from './enrol.js';

test('each enrolment refusal has its own message', () => {
  const seen = new Set();
  for (const code of ['invite_refused', 'invite_used', 'passkey_limit', 'slow_down']) {
    const m = enrolRefusal(code, 403);
    assert.ok(m, code);
    seen.add(m);
  }
  assert.equal(seen.size, 4);
  assert.match(enrolRefusal('invite_used', 403), /already been used/);
  assert.match(enrolRefusal('invite_refused', 403), /expired/);
});

test('a refusal that is not about enrolment is left to the caller', () => {
  assert.equal(enrolRefusal('login_refused', 401), null);
  assert.equal(enrolRefusal(undefined, 500), null);
});

test('the form is trimmed and both fields are required', () => {
  assert.deepEqual(readInviteForm(' owner ', ' abc \n'), { operator: 'owner', invite: 'abc' });
  assert.match(readInviteForm('', 'abc').error, /operator/i);
  assert.match(readInviteForm('owner', '  ').error, /invite/i);
});

test('an operator id is lower case, as the server keeps it', () => {
  assert.deepEqual(readInviteForm('Owner', 'abc'), { operator: 'owner', invite: 'abc' });
});
