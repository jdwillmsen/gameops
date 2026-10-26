// What the sign-in page tells a person for each way a sign-in can fail, and
// what to do next. Kept apart from the page so every code has a test.

const BY_CODE = {
  invite_refused: ['That invite is not valid or was already used.', 'Ask the owner for a new invite.'],
  invite_used: ['That invite was already used.', 'Ask the owner for a new invite.'],
  passkey_limit: ['This operator already has the maximum number of passkeys.', 'Sign in with one you already have, or ask the owner to remove one.'],
  slow_down: ['Too many attempts.', 'Wait a minute and try again.'],
  login_refused: ['That passkey was not recognised.', 'Try another passkey, or ask the owner for an invite to create one on this device.'],
  ceremony_expired: ['That took too long.', 'Try again.'],
  passkey_refused: ['The server did not accept that passkey.', 'Try again; if it keeps happening, ask the owner.'],
  bad_body: ['The sign-in request was malformed.', 'Reload the page and try again.'],
  bad_request: ['The sign-in request was refused.', 'Reload the page and try again.'],
  json_required: ['The sign-in request was refused.', 'Reload the page and try again.'],
  internal: ['The server hit an error.', 'Try again in a moment; if it keeps happening, tell the owner.'],
};

const BY_STATUS = {
  404: ['Sign-in is not turned on for this console.', 'Ask the owner.'],
  429: BY_CODE.slow_down,
  502: ['The console could not be reached.', 'Try again in a moment.'],
  503: ['The console could not be reached.', 'Try again in a moment.'],
  504: ['The console could not be reached.', 'Try again in a moment.'],
};

// A WebAuthn call rejects with a DOMException whose name is all the page
// can trust; its text is the browser's and differs by vendor.
const BY_DOM = {
  login: {
    NotAllowedError: ['No passkey was used.', 'The prompt was cancelled, timed out, or this device has no passkey for this console. Try again, or ask the owner for an invite to create one.'],
    SecurityError: ['This browser would not use a passkey for this address.', 'Open the console at its usual address and try again.'],
    NotSupportedError: ['This browser or device cannot use passkeys.', 'Try another browser or device.'],
    AbortError: ['The passkey prompt was interrupted.', 'Try again.'],
  },
  register: {
    NotAllowedError: ['The passkey was not created.', 'The prompt was cancelled or timed out. Try again.'],
    InvalidStateError: ['This device already has a passkey for this console.', 'Sign in with it instead.'],
    SecurityError: ['This browser would not create a passkey for this address.', 'Open the invite link at the console\'s usual address.'],
    NotSupportedError: ['This browser or device cannot create this kind of passkey.', 'Try another browser or device.'],
    AbortError: ['The passkey prompt was interrupted.', 'Try again.'],
  },
};

const FALLBACK = ['Sign-in failed.', 'Try again; if it keeps happening, tell the owner.'];

// err is whatever the page caught: a refusal carries `status` and `code`, a
// request that never answered `network`, a browser rejection a `name`.
export function explain(err, kind) {
  let pair;
  if (err && err.network) pair = ['The console could not be reached.', 'Check your connection and try again.'];
  else if (err && err.refused) pair = BY_CODE[err.code] || BY_STATUS[err.status] || FALLBACK;
  else if (err && err.name) pair = (BY_DOM[kind] || {})[err.name] || FALLBACK;
  else pair = FALLBACK;
  return { text: pair[0], next: pair[1] };
}

export const CODES = Object.keys(BY_CODE);
