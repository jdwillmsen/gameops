// How the runner comes to hold a service session: where it reads the
// secret from, the exchange, and what it will accept back. Nothing here
// prints, and nothing here returns the secret to its caller.

import { spawn } from 'node:child_process';
import { readFile } from 'node:fs/promises';

export const SESSION_COOKIE = '__Host-mcmap_session';
export const SERVICE_NAME = 'service:e2e';

// The secret is held in this module and handed out to nothing. What may
// not appear in anything written is asked of here instead.
const held = new Set();

// Whatever is about to be written, with anything secret taken out of it.
// Nothing is expected to need this; it is here so that a mistake elsewhere
// prints a marker and not a credential.
export function redact(text) {
  let out = String(text);
  for (const secret of held) if (secret.length >= 8) out = out.split(secret).join('[redacted]');
  return out;
}

// Whether anything about to be written to disk holds the secret or the
// session: asked before it is written, so that neither ever is.
export const holds = (text) => [...held].some((secret) => secret.length >= 8 && String(text).includes(secret));

function run(command, args, { timeoutMs = 20_000 } = {}) {
  return new Promise((resolve, reject) => {
    // Piped, never inherited: what the command prints goes to this
    // process's memory and to no terminal or log.
    const child = spawn(command, args, { stdio: ['ignore', 'pipe', 'pipe'] });
    const out = [];
    let errBytes = 0;
    child.stdout.on('data', (chunk) => out.push(chunk));
    child.stderr.on('data', (chunk) => { errBytes += chunk.length; });
    const timer = setTimeout(() => child.kill('SIGKILL'), timeoutMs);
    child.on('error', (err) => { clearTimeout(timer); reject(new Error(`${command} could not be run: ${err.code || 'error'}`)); });
    child.on('close', (code) => {
      clearTimeout(timer);
      // Its own words are left out: an error from a command that was
      // asked for a secret may quote what it was working on.
      if (code !== 0) reject(new Error(`${command} ${args[0]} ended with ${code} and ${errBytes} bytes of complaint`));
      else resolve(Buffer.concat(out));
    });
  });
}

// "namespace/name/key", the three things that name one value of a Secret.
export function parseSecretRef(ref) {
  const parts = String(ref).split('/');
  if (parts.length !== 3 || !parts.every((part) => /^[a-z0-9]([a-z0-9._-]{0,251}[a-z0-9])?$/i.test(part))) {
    throw new Error('a Secret is named as namespace/name/key');
  }
  return { namespace: parts[0], name: parts[1], key: parts[2] };
}

// Reads the secret from a file, as a pod is given it, or from the cluster
// by asking kubectl in a child whose output is kept. There is on purpose no
// way to pass it as an argument or in a variable: both are things that get
// printed.
async function readSecret({ secretFile, secretRef, kubectl = 'kubectl' }) {
  let raw;
  if (secretFile) {
    raw = (await readFile(secretFile, 'utf8')).trim();
  } else if (secretRef) {
    const { namespace, name, key } = parseSecretRef(secretRef);
    const encoded = await run(kubectl, ['get', 'secret', name, '-n', namespace, '-o', `jsonpath={.data.${key.replaceAll('.', '\\.')}}`]);
    raw = Buffer.from(encoded.toString('ascii'), 'base64').toString('utf8').trim();
  } else {
    throw new Error('no secret source: give --secret-file or --secret-from-kube');
  }
  if (raw.length < 32) throw new Error('the secret read is shorter than any the map accepts');
  held.add(raw);
  return raw;
}

// What a Set-Cookie header says, as a browser would read it.
export function parseSetCookie(header) {
  const [pair, ...rest] = String(header).split(';').map((part) => part.trim());
  const eq = pair.indexOf('=');
  if (eq < 1) throw new Error('the exchange set no cookie that can be read');
  const cookie = { name: pair.slice(0, eq), value: pair.slice(eq + 1), attributes: {} };
  for (const part of rest) {
    const at = part.indexOf('=');
    cookie.attributes[(at < 0 ? part : part.slice(0, at)).toLowerCase()] = at < 0 ? true : part.slice(at + 1);
  }
  return cookie;
}

// The cookie as the browser is to hold it for the public address, or an
// error. It is installed with exactly the attributes the server set, and
// refused if those are any weaker than a player's: the runner never makes
// up for a cookie that came without them.
export function browserCookie(setCookie, publicUrl) {
  const { name, value, attributes } = parseSetCookie(setCookie);
  const wrong = [];
  if (name !== SESSION_COOKIE) wrong.push('its name');
  if (attributes.secure !== true) wrong.push('Secure');
  if (attributes.httponly !== true) wrong.push('HttpOnly');
  if (String(attributes.samesite).toLowerCase() !== 'lax') wrong.push('SameSite');
  if (attributes.path !== '/') wrong.push('Path');
  if ('domain' in attributes) wrong.push('Domain');
  const maxAge = Number(attributes['max-age']);
  if (!Number.isInteger(maxAge) || maxAge < 60 || maxAge > 30 * 60) wrong.push('Max-Age');
  if (wrong.length > 0) throw new Error(`the session cookie is not as a player's is: ${wrong.join(', ')}`);
  const url = new URL(publicUrl);
  held.add(value);
  return {
    name,
    value,
    // By address and not by domain, which is what makes it the host-only
    // cookie the __Host- prefix demands.
    url: `${url.origin}/`,
    httpOnly: true,
    secure: true,
    sameSite: 'Lax',
    expires: Math.floor(Date.now() / 1000) + maxAge,
  };
}

// How an exchange went wrong: reason is one of the suite's own sentences,
// which is what is logged, and the message says more for a person at a
// terminal. Neither holds what was presented.
export class ExchangeError extends Error {
  constructor(reason, more, status = 0) {
    super(`${reason}: ${more}`);
    this.reason = reason;
    this.status = status;
  }
}

// Exchanges the secret for a session on the map's internal listener. The
// secret goes in a header, to an address that carries nothing else.
export async function exchange({ internalUrl, publicUrl, userAgent, fetchImpl = fetch, ...source }) {
  const target = new URL('/internal/v1/service-sessions', internalUrl);
  let secret;
  try {
    secret = await readSecret(source);
  } catch (err) {
    throw new ExchangeError('the secret could not be read', err.message);
  }
  let res;
  try {
    res = await fetchImpl(target, { method: 'POST', headers: { Authorization: `Bearer ${secret}`, 'User-Agent': userAgent }, redirect: 'error' });
  } catch (err) {
    throw new ExchangeError('the exchange could not be reached', `${err.cause?.code || err.name}`);
  }
  if (res.status !== 200) throw new ExchangeError('the exchange refused the secret', `answered ${res.status}`, res.status);
  const body = await res.json();
  if (body.identity !== SERVICE_NAME) throw new ExchangeError('the exchange answered with something that is not a service session', 'not the service identity');
  const cookies = res.headers.getSetCookie();
  if (cookies.length !== 1) throw new ExchangeError('the exchange answered with something that is not a service session', `${cookies.length} cookies`);
  let cookie;
  try {
    cookie = browserCookie(cookies[0], publicUrl);
  } catch (err) {
    throw new ExchangeError('the exchange answered with something that is not a service session', err.message);
  }
  return { cookie, ttlSeconds: body.ttlSeconds, expiresAt: body.expiresAt };
}

// A port-forward to the map's internal port, for a run from outside the
// cluster: "namespace/service:port". Resolves with the local address and a
// way to end it. It changes nothing in the cluster.
export function portForward(ref, { kubectl = 'kubectl' } = {}) {
  const match = /^([a-z0-9-]+)\/([a-z0-9-]+):(\d{1,5})$/.exec(String(ref));
  if (!match) throw new Error('a port-forward is named as namespace/service:port');
  const [, namespace, service, port] = match;
  return new Promise((resolve, reject) => {
    const child = spawn(kubectl, ['port-forward', '-n', namespace, `service/${service}`, `:${port}`, '--address', '127.0.0.1'], { stdio: ['ignore', 'pipe', 'ignore'] });
    let said = '';
    const timer = setTimeout(() => { child.kill(); reject(new Error('the port-forward did not come up')); }, 20_000);
    child.on('error', (err) => { clearTimeout(timer); reject(new Error(`kubectl could not be run: ${err.code || 'error'}`)); });
    child.on('close', (code) => { clearTimeout(timer); reject(new Error(`the port-forward ended with ${code}`)); });
    child.stdout.on('data', (chunk) => {
      said += chunk;
      const local = /Forwarding from 127\.0\.0\.1:(\d+)/.exec(said);
      if (!local) return;
      clearTimeout(timer);
      resolve({ url: `http://127.0.0.1:${local[1]}`, close: () => child.kill() });
    });
  });
}
