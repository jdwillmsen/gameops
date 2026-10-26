// The server sends a browser back from a failed Xbox sign-in with one word
// from a closed list. The word is looked up here and never shown, so
// nothing a caller put in the address can reach the page.
export const SENTENCES = {
  not_allowed: 'That Microsoft account is not one of the operators. Use your passkey, or ask the owner to add it.',
  cancelled: 'Sign-in was cancelled.',
  expired: 'That sign-in took too long or was started in another browser. Try again.',
  state_mismatch: 'That sign-in did not match the one this browser started. Try again.',
  replayed: 'That sign-in was already used. Try again.',
  bad_request: 'Microsoft sent back something the console could not read. Try again.',
  microsoft_error: 'Microsoft could not sign you in. Try again, or use your passkey.',
  exchange_failed: 'The console could not finish signing you in with Microsoft. Use your passkey.',
  xbox_unavailable: 'Xbox is not answering. Try again in a moment, or use your passkey.',
  no_xbox_profile: 'That Microsoft account has no Xbox profile. Create one at xbox.com, or sign in with another account.',
  child_account: 'Xbox does not let a child account sign in here until it is part of a Family. Use your passkey.',
  xbox_refused: 'Xbox would not sign that account in. Use your passkey.',
  bad_identity: 'The console could not tell which account that is. Use your passkey.',
  busy: 'Lots of sign-ins just now. Try again in a minute, or use your passkey.',
  fault: 'The console could not sign you in. Use your passkey.',
};
export const FALLBACK = 'The Xbox sign-in did not work. Use your passkey.';

// Only an own property of the table is a sentence: a word like
// "constructor" or "__proto__" is the fallback, as anything unknown is.
export function sentenceFor(word) {
  return typeof word === 'string' && Object.hasOwn(SENTENCES, word) ? SENTENCES[word] : FALLBACK;
}

// The word the server sent the browser back with, or null if it did not. A
// repeated parameter is somebody's doing and is treated as absent.
export function resultIn(search) {
  const values = new URLSearchParams(search || '').getAll('xbox');
  return values.length === 1 ? values[0] : null;
}

// Whether the server offers Xbox sign-in, asked rather than assumed: a
// deployment without it has no route to send the button to.
export async function xboxOffered(fetchJson) {
  try {
    const methods = await fetchJson('/auth/methods');
    return !!methods && methods.xbox === true;
  } catch {
    return false;
  }
}
