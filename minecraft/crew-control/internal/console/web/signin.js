import { signinParams } from '/static/state.js';

// The invite is a credential: it leaves the address bar and history before
// anything else runs.
const { invite, operator } = signinParams(location.hash);
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
  const res = await fetch(path, {
    method: 'POST',
    headers: { 'Content-Type': 'application/json', 'X-Crew-Request': '1' },
    body: JSON.stringify(body ?? {}),
  });
  let data = null;
  try { data = await res.json(); } catch { /* an error page has no body worth showing */ }
  if (!res.ok) throw new Error(refusal(res.status, data));
  return data;
}

function refusal(status, data) {
  const code = data && data.error;
  if (code === 'invite_refused') return 'That invite is not valid or was already used.';
  if (code === 'passkey_limit') return 'This operator already has the maximum number of passkeys.';
  if (code === 'slow_down' || status === 429) return 'Too many attempts. Wait a moment and try again.';
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

async function guarded(button, work) {
  button.disabled = true;
  say('');
  try {
    await work();
    location.assign('/');
  } catch (err) {
    // The browser's own cancel is a NotAllowedError; its text is not for us.
    say(err.name === 'NotAllowedError' ? 'The passkey prompt was cancelled.' : err.message);
    button.disabled = false;
  }
}

async function register() {
  const { publicKey } = await post('/auth/register/options', { invite, operator });
  const cred = await navigator.credentials.create({ publicKey: creationOptions(publicKey) });
  const r = cred.response;
  await post('/auth/register/finish', {
    id: cred.id,
    rawId: enc(cred.rawId),
    type: cred.type,
    response: { attestationObject: enc(r.attestationObject), clientDataJSON: enc(r.clientDataJSON) },
  });
}

async function login() {
  const { publicKey } = await post('/auth/login/options');
  const cred = await navigator.credentials.get({ publicKey: requestOptions(publicKey) });
  const r = cred.response;
  await post('/auth/login/finish', {
    id: cred.id,
    rawId: enc(cred.rawId),
    type: cred.type,
    response: {
      authenticatorData: enc(r.authenticatorData),
      clientDataJSON: enc(r.clientDataJSON),
      signature: enc(r.signature),
      userHandle: r.userHandle ? enc(r.userHandle) : '',
    },
  });
}

if (!window.PublicKeyCredential) {
  say('This browser does not support passkeys.');
  $('login').disabled = true;
} else {
  $('login').addEventListener('click', () => guarded($('login'), login));
}
if (invite && operator) {
  $('intro').textContent = `Create a passkey for ${operator}.`;
  $('register').hidden = false;
  $('register').addEventListener('click', () => guarded($('register'), register));
}
