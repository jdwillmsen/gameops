// Messages for the invite form on the sign-in page. They never repeat what
// the person typed, since the invite is a credential.
const MESSAGES = {
  invite_refused: 'That invite is not valid or has expired. Check the operator and code, or ask for a new invite.',
  invite_used: 'That invite has already been used. Ask for a new invite.',
  passkey_limit: 'This operator already has the maximum number of passkeys.',
  slow_down: 'Too many attempts. Wait a moment and try again.',
};

export function enrolRefusal(code, status) {
  if (MESSAGES[code]) return MESSAGES[code];
  return status === 429 ? MESSAGES.slow_down : null;
}

// Operator ids are stored lower case; a phone keyboard capitalises the first
// letter, which should not cost anyone an invite.
export function readInviteForm(operator, invite) {
  const op = String(operator).trim().toLowerCase();
  const code = String(invite).trim();
  if (!op) return { error: 'Enter your operator name.' };
  if (!code) return { error: 'Enter your invite code.' };
  return { operator: op, invite: code };
}
