'use strict';

// The passkey login, the account panel and the Admin section, run and not
// read, against a server and an authenticator made of plain functions. Run
// by `go test ./web/` where there is a node, or by hand:
// `node web/testdata/passkey.test.cjs`.
const assert = require('node:assert/strict');
const vm = require('node:vm');
const fs = require('node:fs');
const path = require('node:path');
const { page } = require('./dom.cjs');

const tests = [];
const test = (name, fn) => tests.push([name, fn]);
const source = (file) => fs.readFileSync(path.join(__dirname, '..', file), 'utf8');
const tick = () => new Promise((done) => setTimeout(done, 0));

const json = (status, body) => ({ ok: status < 300, status, json: async () => body });

// A page with passkey.js in it. answers maps "METHOD path" to a function of
// the request body, or to a response; webauthn says whether the browser has
// it and what the authenticator answers; signal says whether the browser
// can be asked to drop a credential. Passkeys are on unless answers says
// otherwise for api/config.
function env({ answers: given = {}, webauthn = true, signal = true, confirm = true } = {}) {
  const answers = { 'GET api/config': json(200, { login: true, passkeys: true }), ...given };
  const { win, doc } = page(new Map(), { files: [] });
  const made = new Map();
  const plain = doc.getElementById;
  const kind = (id) => (/-(passkey|open|close|logout-all|add|confirm|cancel)$/.test(id) ? 'button' : /-(label|code)$/.test(id) ? 'input' : /passkeys$/.test(id) ? 'ul' : 'div');
  doc.getElementById = (id) => plain(id) || (made.has(id) ? made.get(id) : (made.set(id, Object.assign(doc.createElement(kind(id)), { id })), made.get(id)));
  for (const id of ['login-passkey', 'account-open', 'account', 'admin']) doc.getElementById(id).hidden = true;
  const calls = [];
  const signalled = [];
  win.location = { hostname: 'map.example', reload() { calls.push('reload'); } };
  win.confirm = (text) => { calls.push(`confirm ${text}`); return confirm; };
  win.fetch = async (url, init = {}) => {
    const key = `${init.method || 'GET'} ${url}`;
    calls.push(key);
    const answer = answers[key];
    if (typeof answer === 'function') return answer(init.body ? JSON.parse(init.body) : null);
    return answer || json(404, {});
  };
  if (webauthn) {
    win.PublicKeyCredential = function PublicKeyCredential() {};
    if (signal) win.PublicKeyCredential.signalUnknownCredential = async (o) => { signalled.push(o); };
    const cred = (response) => ({ id: 'Y3JlZA', rawId: new Uint8Array([99, 114, 101, 100]).buffer, type: 'public-key', response });
    win.navigator = {
      credentials: {
        create: async () => cred({ clientDataJSON: new Uint8Array([1]).buffer, attestationObject: new Uint8Array([2]).buffer }),
        get: async () => cred({ clientDataJSON: new Uint8Array([1]).buffer, authenticatorData: new Uint8Array([3]).buffer, signature: new Uint8Array([4]).buffer, userHandle: new Uint8Array([5]).buffer }),
      },
    };
  } else {
    win.navigator = {};
  }
  // page() has already made win a context; these are Node's, which a
  // fresh context does not have.
  win.atob = atob;
  win.btoa = btoa;
  vm.runInContext(source('passkey.js'), win, { filename: 'passkey.js' });
  return { win, doc, calls, signalled, el: (id) => doc.getElementById(id) };
}

const options = { publicKey: { challenge: 'AAAA', user: { id: 'AQID', name: 'fwb-map', displayName: 'FWB map admin' }, excludeCredentials: [{ id: 'BAUG', type: 'public-key' }] } };

test('the model: labels, bytes and tries', () => {
  const { labelOK, toBytes, toB64url, triesText } = env().win.mcmapPasskey;
  for (const good of ['Phone', 'Work laptop 2', 'Key-A']) assert.ok(labelOK(good), good);
  for (const bad of ['', '   ', 'Café', 'Phone 📱', 'a'.repeat(33), '<b>x</b>']) assert.ok(!labelOK(bad), bad);
  assert.equal(toB64url(toBytes('AQID-_8')), 'AQID-_8');
  assert.equal(triesText(1), '1 try left');
  assert.equal(triesText(2), '2 tries left');
});

test('the passkey login is offered only when the server and the browser both have it', async () => {
  const on = env({ answers: { 'GET api/config': json(200, { login: true, passkeys: true }) } });
  await on.win.mcmapPasskey.ready;
  assert.equal(on.el('login-passkey').hidden, false);
  const off = env({ answers: { 'GET api/config': json(200, { login: true, passkeys: false }) } });
  await off.win.mcmapPasskey.ready;
  assert.equal(off.el('login-passkey').hidden, true);
  const old = env({ webauthn: false, answers: { 'GET api/config': json(200, { login: true, passkeys: true }) } });
  await old.win.mcmapPasskey.ready;
  assert.equal(old.el('login-passkey').hidden, true, 'a browser with no WebAuthn keeps the code login only');
});

test('logging in with a passkey sends what the authenticator signed and reloads', async () => {
  const sent = [];
  const e = env({ answers: {
    'GET api/config': json(200, { passkeys: true }),
    'POST auth/passkey/options': json(200, options),
    'POST auth/passkey/finish': (body) => { sent.push(body); return json(200, { state: 'ok' }); },
  } });
  await e.win.mcmapPasskey.ready;
  e.el('login-passkey').click();
  for (let i = 0; i < 5; i++) await tick();
  assert.equal(sent.length, 1);
  assert.equal(sent[0].response.userHandle, 'BQ');
  assert.ok(e.calls.includes('reload'));
});

test('the Admin section is there for an admin only', async () => {
  const member = env({ answers: { 'GET api/me': json(200, { gamertag: 'Steve Builds' }), 'GET api/passkeys': json(200, []) } });
  await member.win.mcmapPasskey.ready;
  assert.equal(member.el('admin').hidden, true);
  assert.ok(!member.calls.includes('GET api/admin/status'));
  const admin = env({ answers: {
    'GET api/me': json(200, { gamertag: 'Steve Builds', admin: true }),
    'GET api/passkeys': json(200, []),
    'GET api/admin/status': json(200, { build: 'abc', problem: '', operators: 'live', features: { admin: true } }),
  } });
  await admin.win.mcmapPasskey.ready;
  assert.equal(admin.el('admin').hidden, false);
  assert.match(admin.el('admin-status').textContent, /live/);
});

test('a game-code login on the open page brings the account panel and the Admin section', async () => {
  let me = json(401, {});
  const e = env({ answers: {
    'GET api/me': () => me,
    'GET api/passkeys': json(200, []),
    'GET api/admin/status': json(200, { operators: 'live' }),
  } });
  await e.win.mcmapPasskey.ready;
  assert.equal(e.el('admin').hidden, true);
  assert.equal(e.el('account-open').hidden, true);
  me = json(200, { gamertag: 'Steve Builds', admin: true });
  for (let i = 0; i < 2; i++) {
    e.doc.dispatchEvent(new e.win.CustomEvent('mcmap:login'));
    for (let j = 0; j < 10; j++) await tick();
  }
  assert.equal(e.el('admin').hidden, false);
  assert.equal(e.el('account-open').hidden, false);
  assert.match(e.el('admin-status').textContent, /live/);
  // Wired once however often the page logs in: one click opens the panel.
  e.el('account-open').click();
  assert.equal(e.el('account').hidden, false);
});

function adding(extra = {}) {
  return env({ ...extra, answers: {
    'GET api/me': json(200, { gamertag: 'Steve Builds', admin: true }),
    'GET api/passkeys': json(200, []),
    'GET api/admin/status': json(200, {}),
    'POST auth/passkey/register/options': json(200, options),
    'POST auth/passkey/register/finish': json(200, { fingerprint: '7F3A-91C2', expiresIn: 120 }),
    'POST auth/passkey/register/cancel': json(204, {}),
    ...extra.answers,
  } });
}

async function startAdding(e, label = 'Phone') {
  await e.win.mcmapPasskey.ready;
  e.el('admin-label').value = label;
  e.el('admin-add').click();
  for (let i = 0; i < 5; i++) await tick();
}

test('adding a passkey asks for !map passkey and shows the fingerprint to compare', async () => {
  const e = adding();
  await startAdding(e);
  assert.match(e.el('admin-step').textContent, /does not work yet/);
  assert.match(e.el('admin-step').textContent, /2 minutes/);
  assert.equal(e.el('admin-steps').hidden, false);
  assert.equal(e.el('admin-fingerprint').textContent, '7F3A-91C2');
  assert.equal(e.el('admin-code').hidden, false);
});

test('starting again hides the steps', async () => {
  const e = adding();
  await startAdding(e);
  e.el('admin-cancel').click();
  for (let i = 0; i < 5; i++) await tick();
  assert.equal(e.el('admin-steps').hidden, true);
  assert.equal(e.el('admin-step').textContent, '');
});

test('the command in the steps is copied exactly as it has to be typed', async () => {
  const e = adding();
  const copied = [];
  e.win.navigator.clipboard = { writeText: async (text) => { copied.push(text); } };
  e.el('admin-command').textContent = '!map passkey';
  await startAdding(e);
  e.el('admin-copy').click();
  for (let i = 0; i < 3; i++) await tick();
  assert.deepEqual(copied, ['!map passkey']);
  assert.equal(e.el('admin-copied').textContent, 'Copied');
});

test('a label outside the rules is refused before anything is asked of the server', async () => {
  const e = adding();
  await startAdding(e, 'Café');
  assert.ok(!e.calls.includes('POST auth/passkey/register/options'));
  assert.match(e.el('admin-note').textContent, /1 to 32 letters, digits, spaces or hyphens/);
});

test('a wrong code says how many tries are left, and the third starts again and drops the orphan', async () => {
  let wrong = 0;
  const e = adding({ answers: { 'POST auth/passkey/register/confirm': () => (++wrong < 3 ? json(400, { error: 'wrong_code', triesLeft: 3 - wrong }) : json(410, { error: 'abandoned' })) } });
  await startAdding(e);
  for (const want of [/2 tries left/, /1 try left/, /start again/i]) {
    e.el('admin-code').value = 'ZZZZZZ';
    e.el('admin-confirm').click();
    for (let i = 0; i < 5; i++) await tick();
    assert.match(e.el('admin-note').textContent, want);
  }
  assert.equal(e.signalled.length, 1);
  assert.equal(e.signalled[0].rpId, 'map.example');
});

test('with no signal API the page says a passkey may be left behind', async () => {
  const e = adding({ signal: false, answers: { 'POST auth/passkey/register/confirm': json(404, { error: 'nothing_pending' }) } });
  await startAdding(e);
  e.el('admin-code').value = 'ABC234';
  e.el('admin-confirm').click();
  for (let i = 0; i < 5; i++) await tick();
  assert.match(e.el('admin-note').textContent, /password manager/);
});

test('the confirm request carries the credential this page created', async () => {
  const sent = [];
  const e = adding({ answers: { 'POST auth/passkey/register/confirm': (body) => { sent.push(body); return json(204, {}); } } });
  await startAdding(e);
  e.el('admin-code').value = 'abc234';
  e.el('admin-confirm').click();
  for (let i = 0; i < 5; i++) await tick();
  assert.deepEqual(JSON.parse(JSON.stringify(sent)), [{ credentialId: 'Y3JlZA', code: 'ABC234' }]);
  assert.match(e.el('admin-note').textContent, /added/);
});

test('a label is shown as text and never as markup', async () => {
  const e = env({ answers: { 'GET api/me': json(200, { gamertag: 'Steve Builds' }), 'GET api/passkeys': json(200, [{ id: 'k1', label: '<b>x</b>', created: '2026-10-14T00:00:00Z' }]) } });
  await e.win.mcmapPasskey.ready;
  const list = e.el('account-passkeys');
  assert.equal(list.children.length, 1);
  assert.match(list.children[0].textContent, /<b>x<\/b>/);
});

test('Log out everywhere asks first and says passkeys go too', async () => {
  const e = env({ answers: { 'GET api/me': json(200, { gamertag: 'Steve Builds' }), 'GET api/passkeys': json(200, []), 'POST api/logout-all': json(204, {}) } });
  await e.win.mcmapPasskey.ready;
  e.el('account-logout-all').click();
  for (let i = 0; i < 5; i++) await tick();
  const asked = e.calls.find((c) => c.startsWith('confirm '));
  assert.equal(asked, 'confirm This logs out every browser logged in as you and removes all your passkeys.');
  assert.ok(e.calls.includes('POST api/logout-all'));
  const no = env({ confirm: false, answers: { 'GET api/me': json(200, { gamertag: 'Steve Builds' }), 'GET api/passkeys': json(200, []) } });
  await no.win.mcmapPasskey.ready;
  no.el('account-logout-all').click();
  await tick();
  assert.ok(!no.calls.includes('POST api/logout-all'));
});

const listed = (extra = {}) => env({ ...extra, answers: {
  'GET api/me': json(200, { gamertag: 'Steve Builds' }),
  'GET api/passkeys': json(200, [{ id: 'k1', label: 'Phone', created: '2026-10-14T00:00:00Z' }, { id: 'k2', label: 'Spare', created: '2026-10-14T00:00:00Z', pending: true }]),
  ...extra.answers,
} });

test('a pending passkey is listed but cannot be removed, and a missing lastUsed is not an error', async () => {
  const e = listed();
  await e.win.mcmapPasskey.ready;
  const [done, pending] = e.el('account-passkeys').children;
  assert.equal(done.querySelectorAll('button').length, 1);
  assert.equal(pending.querySelectorAll('button').length, 0);
  assert.match(pending.textContent, /Spare/);
});

test('removing a passkey drops it only when the server says it is gone', async () => {
  const e = listed({ answers: { 'DELETE api/passkeys/k1': json(204, {}) } });
  await e.win.mcmapPasskey.ready;
  e.el('account-passkeys').children[0].querySelectorAll('button')[0].click();
  for (let i = 0; i < 5; i++) await tick();
  assert.ok(e.calls.includes('DELETE api/passkeys/k1'));
  assert.equal(e.el('account-passkeys').children.length, 1);
});

test('a removal that failed is shown as failed and the passkey stays listed', async () => {
  for (const status of [404, 500]) {
    const e = listed({ answers: { 'DELETE api/passkeys/k1': json(status, {}) } });
    await e.win.mcmapPasskey.ready;
    e.el('account-passkeys').children[0].querySelectorAll('button')[0].click();
    for (let i = 0; i < 5; i++) await tick();
    assert.equal(e.el('account-passkeys').children.length, 2, String(status));
    assert.match(e.el('account-note').textContent, /not removed/i, String(status));
  }
});

test('Log out everywhere that failed claims nothing', async () => {
  const e = env({ answers: { 'GET api/me': json(200, { gamertag: 'Steve Builds' }), 'GET api/passkeys': json(200, []), 'POST api/logout-all': json(500, {}) } });
  await e.win.mcmapPasskey.ready;
  e.el('account-logout-all').click();
  for (let i = 0; i < 5; i++) await tick();
  assert.match(e.el('account-note').textContent, /did not finish/i);
  assert.doesNotMatch(e.el('account-note').textContent, /you are logged out/i);
  assert.ok(!e.calls.includes('reload'));
});

test('a passkey is listed with its label, when it was added, and when it was last used or never', async () => {
  const e = listed({ answers: { 'GET api/passkeys': json(200, [{ id: 'k1', label: 'Phone', created: '2026-10-14T00:00:00Z' }, { id: 'k3', label: 'Key-A', created: '2026-10-01T00:00:00Z', lastUsed: '2026-10-13T08:00:00Z' }, { id: 'k2', label: 'Spare', created: '2026-10-14T00:00:00Z', pending: true }]) } });
  await e.win.mcmapPasskey.ready;
  const [a, b, c] = e.el('account-passkeys').children.map((li) => li.textContent);
  assert.match(a, /Phone.*2026-10-14.*never/);
  assert.match(b, /Key-A.*2026-10-01.*2026-10-13/);
  assert.doesNotMatch(b, /never/);
  assert.match(c, /Spare.*waiting/);
});

test('the Admin status shows every field of the payload', async () => {
  const status = { build: 'abc123', go: 'go1.99', now: '2026-10-15T01:02:03Z', problem: '', quiet: true, features: { login: true, passkeys: false }, snapshotAt: '2026-10-15T00:00:00Z', renderedAt: { overworld: '2026-10-14T23:00:00Z' }, assets: { 'app.js': 'sha-1' }, operators: 'live', live: { lastFrameAgeSeconds: { overworld: 1.5 } }, sessions: { issued: 7, passkeyLogins: 3, refused: 2 } };
  const e = env({ answers: { 'GET api/me': json(200, { admin: true }), 'GET api/passkeys': json(200, []), 'GET api/admin/status': json(200, status) } });
  await e.win.mcmapPasskey.ready;
  const text = e.el('admin-status').textContent;
  for (const want of ['build', 'abc123', 'go1.99', '2026-10-15T01:02:03Z', 'problem', 'quiet', 'features', 'login', 'snapshotAt', '2026-10-15T00:00:00Z', 'renderedAt', 'overworld', 'assets', 'app.js', 'sha-1', 'operators', 'live', 'lastFrameAgeSeconds', '1.5', 'sessions', 'issued', '7', 'passkeyLogins', '3', 'refused', '2']) {
    assert.ok(text.includes(want), want);
  }
});

test('two confirms in flight: the passkey is added whichever answer comes first, and nothing is signalled', async () => {
  for (const order of [[204, 404], [404, 204]]) {
    let n = 0;
    const gates = [];
    const e = adding({ answers: { 'POST auth/passkey/register/confirm': () => new Promise((done) => { const status = order[n++]; gates.push(() => done(json(status, status === 404 ? { error: 'nothing_pending' } : {}))); }) } });
    await startAdding(e);
    e.el('admin-code').value = 'ABC234';
    e.el('admin-confirm').dispatchEvent({ type: 'click' });
    e.el('admin-confirm').dispatchEvent({ type: 'click' });
    await tick();
    assert.equal(gates.length, 2);
    gates[0]();
    await tick();
    gates[1]();
    for (let i = 0; i < 8; i++) await tick();
    assert.match(e.el('admin-note').textContent, /added/, String(order));
    assert.equal(e.signalled.length, 0, String(order));
  }
});

test('buttons are disabled while a request is in flight', async () => {
  let release;
  const e = adding({ answers: { 'POST auth/passkey/register/confirm': () => new Promise((done) => { release = () => done(json(204, {})); }) } });
  await startAdding(e);
  e.el('admin-confirm').click();
  await tick();
  for (const id of ['admin-add', 'admin-confirm', 'admin-cancel']) assert.equal(e.el(id).disabled, true, id);
  release();
  for (let i = 0; i < 5; i++) await tick();
  assert.equal(e.el('admin-add').disabled, false);
});

test('a wrong code with no count left says only that it is wrong', async () => {
  const e = adding({ answers: { 'POST auth/passkey/register/confirm': json(400, { error: 'wrong_code' }) } });
  await startAdding(e);
  e.el('admin-code').value = 'ZZZZZZ';
  e.el('admin-confirm').click();
  for (let i = 0; i < 5; i++) await tick();
  assert.equal(e.el('admin-note').textContent, 'That is not the code.');
});

test('a busy or refusing map is said to be so, not unreachable', async () => {
  const login = env({ answers: { 'GET api/config': json(200, { passkeys: true }), 'POST auth/passkey/options': json(503, {}) } });
  await login.win.mcmapPasskey.ready;
  login.el('login-passkey').click();
  for (let i = 0; i < 5; i++) await tick();
  assert.match(login.el('login-passkey-note').textContent, /busy/);
  const e = adding({ answers: { 'POST auth/passkey/register/options': { ok: false, status: 403, json: async () => { throw new Error('not json'); } } } });
  await startAdding(e);
  assert.match(e.el('admin-note').textContent, /not allowed/);
  assert.doesNotMatch(e.el('admin-note').textContent, /reached/);
});

test('with passkeys off there is no Account button and no passkey request, and an admin keeps the status only', async () => {
  for (const config of [{ login: true }, { login: true, passkeys: false }]) {
    const e = env({ answers: {
      'GET api/config': json(200, config),
      'GET api/me': json(200, { gamertag: 'Steve Builds', admin: true }),
      'GET api/passkeys': json(200, []),
      'GET api/admin/status': json(200, { build: 'abc', operators: 'live' }),
      'POST api/logout-all': json(204, {}),
    } });
    await e.win.mcmapPasskey.ready;
    const what = JSON.stringify(config);
    assert.equal(e.el('account-open').hidden, true, what);
    assert.ok(!e.calls.includes('GET api/passkeys'), what);
    e.el('account-logout-all').click();
    for (let i = 0; i < 5; i++) await tick();
    assert.ok(!e.calls.some((c) => c.startsWith('confirm ')), what);
    assert.ok(!e.calls.includes('POST api/logout-all'), what);
    assert.equal(e.el('admin').hidden, false, what);
    assert.match(e.el('admin-status').textContent, /live/, what);
    assert.equal(e.el('admin-passkey').hidden, true, what);
    e.el('admin-label').value = 'Phone';
    e.el('admin-add').click();
    for (let i = 0; i < 5; i++) await tick();
    assert.ok(!e.calls.includes('POST auth/passkey/register/options'), what);
  }
});

test('the account panel opens and closes on its own', async () => {
  const e = env({ answers: { 'GET api/me': json(200, { gamertag: 'Steve Builds' }), 'GET api/passkeys': json(200, []) } });
  await e.win.mcmapPasskey.ready;
  e.el('account-open').click();
  assert.equal(e.el('account').hidden, false);
  e.el('account-close').click();
  assert.equal(e.el('account').hidden, true);
});

test('logging in with a pending passkey explains that in-game confirmation is needed', async () => {
  const e = env({ answers: {
    'GET api/config': json(200, { passkeys: true }),
    'POST auth/passkey/options': json(200, options),
    'POST auth/passkey/finish': json(401, { error: 'pending' }),
  } });
  await e.win.mcmapPasskey.ready;
  e.el('login-passkey').click();
  for (let i = 0; i < 5; i++) await tick();
  assert.match(e.el('login-passkey-note').textContent, /waiting for in-game confirmation: type !map passkey/i);
});

test('logging in with a non-operator passkey explains operator requirement', async () => {
  const e = env({ answers: {
    'GET api/config': json(200, { passkeys: true }),
    'POST auth/passkey/options': json(200, options),
    'POST auth/passkey/finish': json(401, { error: 'not_operator' }),
  } });
  await e.win.mcmapPasskey.ready;
  e.el('login-passkey').click();
  for (let i = 0; i < 5; i++) await tick();
  assert.match(e.el('login-passkey-note').textContent, /not currently an operator/i);
});

test('a pending passkey error from register options offers the cancel button', async () => {
  const e = adding({ answers: {
    'POST auth/passkey/register/options': json(409, { error: 'pending' }),
  } });
  await e.win.mcmapPasskey.ready;
  e.el('admin-label').value = 'Phone';
  e.el('admin-add').click();
  for (let i = 0; i < 5; i++) await tick();
  assert.match(e.el('admin-note').textContent, /already waiting/i);
  assert.equal(e.el('admin-cancel').hidden, false);
  e.el('admin-cancel').click();
  for (let i = 0; i < 5; i++) await tick();
  assert.ok(e.calls.includes('POST auth/passkey/register/cancel'));
  assert.equal(e.el('admin-cancel').hidden, true);
});

async function main() {
  let failed = 0;
  for (const [name, fn] of tests) {
    try {
      await fn();
      console.log(`ok   ${name}`);
    } catch (err) {
      failed += 1;
      console.log(`FAIL ${name}\n     ${String(err && err.stack ? err.stack : err).split('\n').slice(0, 6).join('\n     ')}`);
    }
  }
  console.log(`${tests.length - failed} of ${tests.length} passed`);
  process.exitCode = failed === 0 ? 0 : 1;
}

main();
