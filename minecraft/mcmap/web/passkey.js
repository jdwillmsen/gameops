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

  // Asked once: the login button and the account panel both follow it.
  const configured = (async () => {
    try { return await (await fetch('api/config', { cache: 'no-store' })).json(); } catch { return null; }
  })();

  async function loginButton() {
    const button = $('login-passkey');
    if (!button) return;
    const config = await configured;
    if (!config) return;
    button.hidden = !(config.passkeys && webauthn());
    button.addEventListener('click', async () => {
      try {
        const got = await post('auth/passkey/options');
        if (!got.ok) { say('login-passkey-note', got.status === 503 ? 'The map is busy. Try again in a moment.' : 'The map could not start a passkey login. Use the code from the game instead.'); return; }
        const opts = await got.json();
        const cred = await navigator.credentials.get(requestOptions(opts));
        const res = await post('auth/passkey/finish', credentialJSON(cred));
        if (res.ok) { window.location.reload(); return; }
        let body = {};
        try { body = await res.json(); } catch { /* ignore */ }
        if (body && body.error === 'pending') {
          say('login-passkey-note', 'This passkey is waiting for in-game confirmation: type !map passkey in the game and enter the code in the browser that added it. Otherwise log in with a game code, then cancel it and add it again.');
          return;
        }
        if (body && body.error === 'not_operator') {
          say('login-passkey-note', 'This passkey belongs to a player who is not currently an operator.');
          return;
        }
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

  const day = (t) => (t ? String(t).slice(0, 10) : 'unknown');

  function listPasskeys(list) {
    const ul = $('account-passkeys');
    if (!ul || !Array.isArray(list)) return;
    ul.replaceChildren(...list.map((k) => {
      const li = document.createElement('li');
      li.textContent = `${k.label}: added ${day(k.created)}, last used ${k.lastUsed ? day(k.lastUsed) : 'never'}${k.pending ? ', waiting for the code' : ''}`;
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

  // Every field the status carries, one row each, so that nothing the
  // server reports is left for a reader to guess at.
  function statusRows(value, name, out) {
    if (value && typeof value === 'object') {
      for (const [k, v] of Object.entries(value)) statusRows(v, name ? `${name}.${k}` : k, out);
    } else {
      out.push([name, value === '' ? 'none' : String(value)]);
    }
    return out;
  }

  function showStatus(s) {
    const box = $('admin-status');
    if (!box) return;
    box.replaceChildren(...statusRows(s, '', []).map(([k, v]) => {
      const row = document.createElement('div');
      row.textContent = `${k}: ${v}`;
      return row;
    }));
  }

  const refusal = (status) => (status === 401 ? 'You are logged out. Reload the page and log in again.'
    : status === 403 ? 'The map says you are not allowed to do that.'
      : status === 503 ? 'The map is busy. Try again in a moment.'
        : 'The map could not do that. Try again.');

  function adminSection() {
    let created = null;
    // Confirms whose answer has not come back. A second one must not read
    // its 404 as the passkey being gone while the first is activating it.
    const confirms = new Set();
    const buttons = () => ['admin-add', 'admin-confirm', 'admin-cancel'].map($);
    let flying = 0;
    const flight = async (fn) => {
      flying += 1;
      for (const b of buttons()) b.disabled = true;
      try { return await fn(); } finally {
        flying -= 1;
        if (flying === 0) for (const b of buttons()) b.disabled = false;
      }
    };
    const entry = (on) => { for (const id of ['admin-steps', 'admin-code', 'admin-confirm', 'admin-cancel']) $(id).hidden = !on; };
    const restart = (text) => {
      const orphan = created;
      created = null;
      entry(false);
      say('admin-fingerprint', '');
      say('admin-step', '');
      say('admin-note', text);
      return orphan;
    };
    $('admin-add').addEventListener('click', () => flight(async () => {
      const label = $('admin-label').value;
      if (!labelOK(label)) { say('admin-note', LABEL_RULE); return; }
      say('admin-note', '');
      let opts;
      try {
        opts = await post('auth/passkey/register/options', { label });
        if (!opts.ok) {
          let why = '';
          try { why = (await opts.json()).error; } catch { /* judged by status below */ }
          if (why === 'pending') {
            say('admin-note', 'A passkey is already waiting for its code. Finish or cancel it first.');
            $('admin-cancel').hidden = false;
            return;
          }
          say('admin-note', why === 'label' ? LABEL_RULE : refusal(opts.status));
          return;
        }
        opts = await opts.json();
      } catch { say('admin-note', 'The map could not be reached.'); return; }
      let cred;
      try { cred = await navigator.credentials.create(creationOptions(opts)); } catch { say('admin-note', 'No passkey was made.'); return; }
      try {
        const fin = await post('auth/passkey/register/finish', credentialJSON(cred));
        if (!fin.ok) { say('admin-note', `${refusal(fin.status)} Start again.`); await signalOrphan(cred.id); return; }
        const { fingerprint, expiresIn } = await fin.json();
        created = cred.id;
        say('admin-fingerprint', fingerprint);
        // The browser has already said the passkey is saved; it is not usable
        // until the code from the game confirms it.
        const minutes = Math.max(1, Math.round((Number(expiresIn) || 120) / 60));
        say('admin-step', `Your browser saved the passkey, but it does not work yet. Finish these steps within ${minutes} ${minutes === 1 ? 'minute' : 'minutes'}:`);
        entry(true);
      } catch { say('admin-note', 'The map could not be reached. Start again.'); await signalOrphan(cred.id); }
    }));
    $('admin-confirm').addEventListener('click', () => flight(async () => {
      const id = created;
      if (!id) return;
      const code = String($('admin-code').value || '').trim().toUpperCase();
      const sent = post('auth/passkey/register/confirm', { credentialId: id, code });
      confirms.add(sent);
      let res;
      try { res = await sent; } catch { confirms.delete(sent); say('admin-note', 'The map could not be reached. Try the code again.'); return; }
      confirms.delete(sent);
      if (res.status === 204) {
        if (created !== id) return;
        restart('Passkey added.');
        refreshList();
        return;
      }
      if (created !== id) return;
      let body = {};
      try { body = await res.json(); } catch { /* an answer with no body is judged by its status */ }
      if (body.error === 'wrong_code') {
        say('admin-note', Number.isInteger(body.triesLeft) ? `That is not the code. ${triesText(body.triesLeft)}.` : 'That is not the code.');
        return;
      }
      if (res.status !== 410 && res.status !== 404) { say('admin-note', refusal(res.status)); return; }
      const others = await Promise.all([...confirms].map((p) => p.then((r) => r.status, () => 0)));
      if (others.includes(204) || created !== id) return;
      const orphan = restart('That passkey is gone. Start again.');
      await signalOrphan(orphan);
    }));
    $('admin-cancel').addEventListener('click', () => flight(async () => {
      const id = created;
      try { await post('auth/passkey/register/cancel'); } catch { /* the pending passkey expires by itself */ }
      if (id && created !== id) return;
      const orphan = restart('');
      if (orphan) await signalOrphan(orphan);
      await refreshList();
    }));
  }

  async function refreshList() {
    try { listPasskeys(await (await fetch('api/passkeys', { cache: 'no-store' })).json()); } catch { /* the list is stale until the next load */ }
  }

  // Shown once: account() runs again after a login, and a second run must
  // not wire the buttons twice.
  let shown = false;
  async function account() {
    if (shown) return;
    const config = await configured;
    // The account panel is passkeys and the Log out everywhere that removes
    // them; with passkeys off there is neither, and !map logout remains.
    const passkeys = Boolean(config && config.passkeys);
    let me;
    try {
      const res = await fetch('api/me', { cache: 'no-store' });
      if (!res.ok) return;
      me = await res.json();
    } catch { return; }
    if (me.service || shown) return;
    shown = true;
    if (passkeys) await accountPanel();
    if (!me.admin) return;
    $('admin').hidden = false;
    $('admin-passkey').hidden = !passkeys;
    if (passkeys) adminSection();
    try { showStatus(await (await fetch('api/admin/status', { cache: 'no-store' })).json()); } catch { /* the section is shown without the status */ }
  }

  async function accountPanel() {
    $('account-open').hidden = false;
    $('account-open').addEventListener('click', () => { $('account').hidden = !$('account').hidden; });
    $('account-close').addEventListener('click', () => { $('account').hidden = true; });
    $('account-logout-all').addEventListener('click', async () => {
      if (!window.confirm('This logs out every browser logged in as you and removes all your passkeys.')) return;
      try {
        const res = await post('api/logout-all');
        if (res.ok) { window.location.reload(); return; }
        say('account-note', 'Log out everywhere did not finish. Some browsers may still be logged in and your passkeys may remain. Try again.');
      } catch {
        say('account-note', 'Log out everywhere did not finish: the map could not be reached. Try again.');
      }
    });
    await refreshList();
  }

  api.ready = Promise.all([loginButton(), account()]);
  // A game-code login unlocks this page without reloading it.
  document.addEventListener('mcmap:login', account);
})();
