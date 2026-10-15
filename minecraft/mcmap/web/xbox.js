'use strict';

// Signing in with an Xbox account. The button is a plain form that goes to the server,
// which does all of it; this script only decides whether to offer the button,
// and says in a sentence why a person was sent back without a login. The
// address carries one word from a short list the server writes, and that
// word is looked up here, never shown.
(() => {
  const SENTENCES = {
    stranger: 'That Microsoft account is not one this server knows. Join the server and type !map in chat once, then try again.',
    cancelled: 'Sign-in was cancelled.',
    expired: 'That sign-in took too long or was started in another browser. Try again.',
    state_mismatch: 'That sign-in did not match the one this browser started. Try again.',
    replayed: 'That sign-in was already used. Try again.',
    bad_request: 'Microsoft sent back something the map could not read. Try again.',
    microsoft_error: 'Microsoft could not sign you in. Try again, or use the code from the game.',
    exchange_failed: 'The map could not finish signing you in with Microsoft. Use the code from the game.',
    xbox_unavailable: 'Xbox is not answering. Try again in a moment, or use the code from the game.',
    no_xbox_profile: 'That Microsoft account has no Xbox profile. Create one at xbox.com, or sign in with another account.',
    child_account: 'Xbox does not let a child account sign in here until it is part of a Family. Use the code from the game.',
    xbox_refused: 'Xbox would not sign that account in. Use the code from the game.',
    bad_identity: 'The map could not tell which player that account is. Use the code from the game.',
    busy: 'Lots of people are signing in just now. Try again in a minute, or use the code from the game.',
    allowlist_unavailable: 'The map could not check the server just now. Use the code from the game, or try again in a minute.',
    fault: 'The map could not sign you in. Use the code from the game.',
  };
  const FALLBACK = 'The Xbox sign-in did not work. Use the code from the game.';

  // Only an own property of the table is a sentence: a word like
  // "constructor" or "__proto__" is the fallback, as anything unknown is.
  const sentenceFor = (word) => (typeof word === 'string' && Object.hasOwn(SENTENCES, word) ? SENTENCES[word] : FALLBACK);

  // The word the server sent the browser back with, or null if it did not.
  function resultIn(search) {
    const values = new URLSearchParams(search || '').getAll('xbox');
    return values.length === 1 ? values[0] : null;
  }

  // The same address with the word taken out, so that a reload does not
  // show the sentence again.
  function without(search) {
    const params = new URLSearchParams(search || '');
    params.delete('xbox');
    const rest = params.toString();
    return rest === '' ? '' : `?${rest}`;
  }

  const api = { sentenceFor, resultIn, without, sentences: SENTENCES, ready: null };
  window.mcmapXbox = api;

  const $ = (id) => document.getElementById(id);

  api.ready = (async () => {
    const offer = $('login-xbox');
    const note = $('login-xbox-note');
    const word = resultIn(window.location.search);
    if (word !== null) {
      if (note) note.textContent = sentenceFor(word);
      try {
        window.history.replaceState(null, '', window.location.pathname + without(window.location.search) + window.location.hash);
      } catch { /* the sentence stays up; a reload shows it again */ }
    }
    if (!offer) return;
    let config = null;
    try { config = await (await fetch('api/config', { cache: 'no-store' })).json(); } catch { /* no button without an answer */ }
    offer.hidden = !(config && config.xbox === true);
  })();
})();
