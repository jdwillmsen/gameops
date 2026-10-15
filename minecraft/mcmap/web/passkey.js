'use strict';

// The passkey login, the account panel and the Admin section. Everything a
// person typed (a label, a code) is shown with textContent and never as
// markup, and nothing here holds a credential: the authenticator keeps the
// key and the server keeps the session cookie, which script cannot read.
(() => {
  // --- the model, with no page in it ------------------------------------------

  const LABEL = /^[A-Za-z0-9 -]{1,32}$/;
  const labelOK = (s) => typeof s === 'string' && LABEL.test(s) && s.trim() !== '';
  const LABEL_RULE = 'A label is 1 to 32 letters, digits, spaces or hyphens.';

  function toBytes(s) {
    const b = atob(String(s).replace(/-/g, '+').replace(/_/g, '/').padEnd(Math.ceil(String(s).length / 4) * 4, '='));
    return Uint8Array.from(b, (c) => c.charCodeAt(0));
  }
  function toB64url(buf) {
    const bytes = new Uint8Array(buf);
    let s = '';
    for (const b of bytes) s += String.fromCharCode(b);
    return btoa(s).replace(/\+/g, '-').replace(/\//g, '_').replace(/=+$/, '');
  }
  function creationOptions(o) {
    const pk = { ...o.publicKey };
    pk.challenge = toBytes(pk.challenge);
    pk.user = { ...pk.user, id: toBytes(pk.user.id) };
    pk.excludeCredentials = (pk.excludeCredentials || []).map((c) => ({ ...c, id: toBytes(c.id) }));
    return { publicKey: pk };
  }
  function requestOptions(o) {
    const pk = { ...o.publicKey };
    pk.challenge = toBytes(pk.challenge);
    pk.allowCredentials = (pk.allowCredentials || []).map((c) => ({ ...c, id: toBytes(c.id) }));
    return { publicKey: pk };
  }
  function credentialJSON(cred) {
    const r = cred.response;
    const response = { clientDataJSON: toB64url(r.clientDataJSON) };
    for (const field of ['attestationObject', 'authenticatorData', 'signature', 'userHandle']) {
      if (r[field]) response[field] = toB64url(r[field]);
    }
    return { id: cred.id, rawId: toB64url(cred.rawId), type: cred.type, response };
  }
  const triesText = (n) => `${n} ${n === 1 ? 'try' : 'tries'} left`;

  const api = { labelOK, toBytes, toB64url, creationOptions, requestOptions, credentialJSON, triesText, ready: null };
  window.mcmapPasskey = api;

  // --- the page --------------------------------------------------------------

  const $ = (id) => document.getElementById(id);
  const say = (id, text) => { const el = $(id); if (el) el.textContent = text; };
  const post = (url, body) => fetch(url, { method: 'POST', headers: body ? { 'Content-Type': 'application/json' } : {}, body: body ? JSON.stringify(body) : undefined });
  const webauthn = () => typeof window.PublicKeyCredential === 'function' && window.navigator && window.navigator.credentials;

  async function loginButton() {
    const button = $('login-passkey');
    if (!button) return;
    let config = {};
    try { config = await (await fetch('api/config', { cache: 'no-store' })).json(); } catch { return; }
    button.hidden = !(config.passkeys && webauthn());
    button.addEventListener('click', async () => {
      try {
        const opts = await (await post('auth/passkey/options')).json();
        const cred = await navigator.credentials.get(requestOptions(opts));
        const res = await post('auth/passkey/finish', credentialJSON(cred));
        if (res.ok) { window.location.reload(); return; }
        say('login-passkey-note', 'That passkey did not log you in. Use the code from the game instead.');
      } catch {
        say('login-passkey-note', 'No passkey was used.');
      }
    });
  }

  async function signalOrphan(id) {
    const P = window.PublicKeyCredential;
    if (P && typeof P.signalUnknownCredential === 'function') {
      try { await P.signalUnknownCredential({ rpId: window.location.hostname, credentialId: id }); return; } catch { /* fall through to the note */ }
    }
    say('admin-note', 'Start again. A passkey for this map may be left in your password manager; you can delete it there.');
  }

  function listPasskeys(list) {
    const ul = $('account-passkeys');
    if (!ul || !Array.isArray(list)) return;
    ul.replaceChildren(...list.map((k) => {
      const li = document.createElement('li');
      li.textContent = `${k.label}${k.pending ? ' (waiting for the code)' : ''}`;
      // A pending passkey is not removable by id; it is cancelled where it was added.
      if (k.pending) return li;
      const remove = document.createElement('button');
      remove.type = 'button';
      remove.textContent = 'Remove';
      remove.addEventListener('click', async () => {
        try {
          const res = await fetch(`api/passkeys/${encodeURIComponent(k.id)}`, { method: 'DELETE' });
          if (res.status === 204) { li.remove(); say('account-note', ''); return; }
          say('account-note', res.status === 404 ? 'That passkey was not removed: the map does not know it.' : 'That passkey was not removed: the map could not save the change. Try again.');
        } catch {
          say('account-note', 'That passkey was not removed: the map could not be reached.');
        }
      });
      li.append(remove);
      return li;
    }));
  }

  function showStatus(s) {
    const box = $('admin-status');
    if (!box) return;
    const rows = [['Build', s.build], ['Problem', s.problem || 'none'], ['Quiet window', s.quiet ? 'yes' : 'no'], ['Operators from', s.operators]];
    box.replaceChildren(...rows.map(([k, v]) => {
      const row = document.createElement('div');
      row.textContent = `${k}: ${v === undefined ? '' : v}`;
      return row;
    }));
  }

  function adminSection() {
    let created = null;
    const entry = (on) => { for (const id of ['admin-code', 'admin-confirm', 'admin-cancel']) $(id).hidden = !on; };
    const restart = (text) => {
      const orphan = created;
      created = null;
      entry(false);
      say('admin-fingerprint', '');
      say('admin-step', '');
      say('admin-note', text);
      return orphan;
    };
    $('admin-add').addEventListener('click', async () => {
      const label = $('admin-label').value;
      if (!labelOK(label)) { say('admin-note', LABEL_RULE); return; }
      say('admin-note', '');
      let opts;
      try {
        opts = await post('auth/passkey/register/options', { label });
        if (!opts.ok) {
          const why = (await opts.json()).error;
          say('admin-note', why === 'pending' ? 'A passkey is already waiting for its code. Finish or cancel it first.' : why === 'label' ? LABEL_RULE : 'The map could not start that. Try again.');
          return;
        }
        opts = await opts.json();
      } catch { say('admin-note', 'The map could not be reached.'); return; }
      let cred;
      try { cred = await navigator.credentials.create(creationOptions(opts)); } catch { say('admin-note', 'No passkey was made.'); return; }
      try {
        const fin = await post('auth/passkey/register/finish', credentialJSON(cred));
        if (!fin.ok) { say('admin-note', 'The map did not accept that passkey. Start again.'); await signalOrphan(cred.id); return; }
        const { fingerprint } = await fin.json();
        created = cred.id;
        say('admin-fingerprint', fingerprint);
        say('admin-step', `Now type !map passkey in the game. You will be whispered a code to type here. Check that the whisper shows the same fingerprint, ${fingerprint}.`);
        entry(true);
      } catch { say('admin-note', 'The map could not be reached. Start again.'); await signalOrphan(cred.id); }
    });
    $('admin-confirm').addEventListener('click', async () => {
      const code = String($('admin-code').value || '').trim().toUpperCase();
      let res;
      try { res = await post('auth/passkey/register/confirm', { credentialId: created, code }); } catch { say('admin-note', 'The map could not be reached. Try the code again.'); return; }
      if (res.status === 204) { restart('Passkey added.'); refreshList(); return; }
      let body = {};
      try { body = await res.json(); } catch { /* an answer with no body is judged by its status */ }
      if (body.error === 'wrong_code') { say('admin-note', `That is not the code. ${triesText(body.triesLeft)}.`); return; }
      if (res.status !== 410 && res.status !== 404) { say('admin-note', 'The map could not check that code. Try again.'); return; }
      const orphan = restart('That passkey is gone. Start again.');
      if (orphan) await signalOrphan(orphan);
    });
    $('admin-cancel').addEventListener('click', async () => {
      try { await post('auth/passkey/register/cancel'); } catch { /* the pending passkey expires by itself */ }
      const orphan = restart('');
      if (orphan) await signalOrphan(orphan);
    });
  }

  async function refreshList() {
    try { listPasskeys(await (await fetch('api/passkeys', { cache: 'no-store' })).json()); } catch { /* the list is stale until the next load */ }
  }

  async function account() {
    let me;
    try {
      const res = await fetch('api/me', { cache: 'no-store' });
      if (!res.ok) return;
      me = await res.json();
    } catch { return; }
    if (me.service) return;
    $('account-open').hidden = false;
    $('account-open').addEventListener('click', () => { $('account').hidden = !$('account').hidden; });
    $('account-logout-all').addEventListener('click', async () => {
      if (!window.confirm('This logs out every browser logged in as you and removes all your passkeys.')) return;
      try {
        const res = await post('api/logout-all');
        if (res.ok) { window.location.reload(); return; }
        say('account-note', 'You are logged out everywhere, but your passkeys were not removed. Log in again and remove them one by one.');
      } catch {
        say('account-note', 'The map could not be reached. Nothing was changed.');
      }
    });
    await refreshList();
    if (!me.admin) return;
    $('admin').hidden = false;
    adminSection();
    try { showStatus(await (await fetch('api/admin/status', { cache: 'no-store' })).json()); } catch { /* the section still offers adding a passkey */ }
  }

  api.ready = Promise.all([loginButton(), account()]);
})();
