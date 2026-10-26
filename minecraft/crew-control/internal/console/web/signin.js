import { signinParams } from '/static/state.js';
import { enrolRefusal, readInviteForm } from '/static/enrol.js';

// The invite is a credential: it leaves the address bar and history before
// anything else runs.
const prefill = signinParams(location.hash);
history.replaceState(null, '', location.pathname);

const $ = id => document.getElementById(id);
const say = text => { $('msg').textContent = text; };

const enc = bytes => {
  let s = '';
  for (const b of new Uint8Array(bytes)) s += String.fromCharCode(b);
  return btoa(s).replaceAll('+', '-').replaceAll('/', '_').replaceAll('=', '');
};
const dec = s => {
  const p = s.replaceAll('-', '+').replaceAll('_', '/');
  return Uint8Array.from(atob(p + '='.repeat((4 - (p.length % 4)) % 4)), c => c.charCodeAt(0)).buffer;
};

async function post(path, body) {
  let res;
  try {
    res = await fetch(path, {
      method: 'POST',
      headers: { 'Content-Type': 'application/json', 'X-Crew-Request': '1' },
      body: JSON.stringify(body ?? {}),
    });
  } catch (err) {
    err.network = true;
    throw err;
  }
  let data = null;
  try { data = await res.json(); } catch { /* an error page has no body worth showing */ }
  if (!res.ok) throw Object.assign(new Error(refusal(res.status, data)), { refused: true });
  return data;
}

// Tags a failure with the step it came from, so the server log can say
// which step of the ceremony the browser gave up at.
async function staged(stage, call) {
  try {
    return await call();
  } catch (err) {
    err.stage = stage;
    throw err;
  }
}

// The server can see a refusal it sent, but not a browser that refused the
// passkey prompt or a request that never arrived. Only a stage and an error
// name leave the page, never a message, id or response.
function report(kind, err) {
  const error = err.refused ? 'refused' : err.network ? 'network' : err instanceof DOMException ? err.name : 'other';
  post('/auth/report', { kind, stage: err.stage || 'finish', error }).catch(() => {});
}

function refusal(status, data) {
  const code = data && data.error;
  const enrol = enrolRefusal(code, status);
  if (enrol) return enrol;
  if (code === 'login_refused') return 'That passkey is not recognised.';
  if (code === 'ceremony_expired') return 'That took too long. Try again.';
  return `Sign-in failed (${status}).`;
}

function creationOptions(pk) {
  return {
    ...pk,
    challenge: dec(pk.challenge),
    user: { ...pk.user, id: dec(pk.user.id) },
    excludeCredentials: (pk.excludeCredentials || []).map(c => ({ ...c, id: dec(c.id) })),
  };
}

function requestOptions(pk) {
  return {
    ...pk,
    challenge: dec(pk.challenge),
    allowCredentials: (pk.allowCredentials || []).map(c => ({ ...c, id: dec(c.id) })),
  };
}

async function guarded(button, kind, work) {
  if (button.disabled) return;
  button.disabled = true;
  say('');
  try {
    await work();
    location.assign('/');
  } catch (err) {
    // The browser's own cancel is a NotAllowedError; its text is not for us.
    say(err.name === 'NotAllowedError' ? 'The passkey prompt was cancelled.' : err.message);
    report(kind, err);
    button.disabled = false;
  }
}

async function register({ operator, invite }) {
  const { publicKey } = await staged('begin', () => post('/auth/register/options', { invite, operator }));
  const cred = await staged('credential', () => navigator.credentials.create({ publicKey: creationOptions(publicKey) }));
  const r = cred.response;
  await staged('finish', () => post('/auth/register/finish', {
    id: cred.id,
    rawId: enc(cred.rawId),
    type: cred.type,
    response: { attestationObject: enc(r.attestationObject), clientDataJSON: enc(r.clientDataJSON) },
  }));
}

async function login() {
  const { publicKey } = await staged('begin', () => post('/auth/login/options'));
  const cred = await staged('credential', () => navigator.credentials.get({ publicKey: requestOptions(publicKey) }));
  const r = cred.response;
  await staged('finish', () => post('/auth/login/finish', {
    id: cred.id,
    rawId: enc(cred.rawId),
    type: cred.type,
    response: {
      authenticatorData: enc(r.authenticatorData),
      clientDataJSON: enc(r.clientDataJSON),
      signature: enc(r.signature),
      userHandle: r.userHandle ? enc(r.userHandle) : '',
    },
  }));
}

if (!window.PublicKeyCredential) {
  say('This browser does not support passkeys.');
  $('login').disabled = true;
  $('register').disabled = true;
} else {
  $('login').addEventListener('click', () => guarded($('login'), 'login', login));
}
$('invite-form').addEventListener('submit', event => {
  event.preventDefault();
  const form = readInviteForm($('operator').value, $('invite').value);
  if (form.error) {
    say(form.error);
    return;
  }
  guarded($('register'), 'register', () => register(form));
});
if (prefill.invite || prefill.operator) {
  $('operator').value = prefill.operator;
  $('invite').value = prefill.invite;
  $('enrol').open = true;
  $('intro').textContent = prefill.operator ? `Create a passkey for ${prefill.operator}.` : 'Enter your invite to create a passkey.';
}
