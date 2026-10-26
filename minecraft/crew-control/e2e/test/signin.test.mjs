// The whole sign-in flow in a real browser: enrol with an invite, reload,
// sign out, sign back in. The browser's virtual authenticator stands in for
// a passkey; the invite is a made-up file the harness script wrote.

import { test, before, after } from 'node:test';
import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import { createServer } from 'node:https';
import { request } from 'node:http';
import path from 'node:path';
import { chromium } from 'playwright-core';

const upstream = process.env.E2E_UPSTREAM;
const tlsPort = Number(process.env.E2E_TLS_PORT);
const work = process.env.E2E_WORK;
assert.ok(upstream && tlsPort && work, 'run through local.sh');
const [upHost, upPort] = upstream.split(':');
const ORIGIN = 'https://crew.test';
const invite = readFileSync(path.join(work, 'invites', 'owner'), 'utf8').trim();

let proxy, browser;

before(async () => {
  // Passkeys need an https origin with no port, so a name that is only
  // known to this browser is pointed at a TLS front for the plain server.
  proxy = createServer({ key: readFileSync(path.join(work, 'key.pem')), cert: readFileSync(path.join(work, 'cert.pem')) }, (req, res) => {
    const up = request({ host: upHost, port: upPort, method: req.method, path: req.url, headers: { ...req.headers, host: 'crew.test' } }, r => {
      res.writeHead(r.statusCode, r.headers);
      r.pipe(res);
    });
    up.on('error', () => { res.writeHead(502).end(); });
    req.pipe(up);
  });
  await new Promise(resolve => proxy.listen(tlsPort, '127.0.0.1', resolve));
  browser = await chromium.launch({
    args: [`--host-resolver-rules=MAP crew.test:443 127.0.0.1:${tlsPort}`, '--ignore-certificate-errors'],
  });
});

after(async () => {
  await browser?.close();
  proxy?.closeAllConnections();
  await new Promise(resolve => (proxy ? proxy.close(resolve) : resolve()));
});

async function newSession() {
  const context = await browser.newContext({ ignoreHTTPSErrors: true });
  const page = await context.newPage();
  const cdp = await context.newCDPSession(page);
  await cdp.send('WebAuthn.enable');
  const { authenticatorId } = await cdp.send('WebAuthn.addVirtualAuthenticator', {
    options: {
      protocol: 'ctap2', transport: 'internal', hasResidentKey: true,
      hasUserVerification: true, isUserVerified: true, automaticPresenceSimulation: true,
    },
  });
  return { context, page, cdp, authenticatorId };
}

// Waits for the console, and says what the page complained of if it never comes.
async function reachConsole(page) {
  try {
    await page.waitForURL(`${ORIGIN}/`, { timeout: 10_000 });
  } catch (err) {
    const msg = await page.textContent('#msg').catch(() => '');
    throw new Error(`sign-in did not reach the console; page said: ${JSON.stringify(msg)}`, { cause: err });
  }
}

async function who(page) {
  await page.waitForSelector('#session .who');
  return (await page.locator('#session .who').textContent()).trim();
}

test('sign-in, reload, restart and sign-out', async () => {
  const { context, page, cdp, authenticatorId } = await newSession();
  try {
    await page.goto(`${ORIGIN}/`);
    assert.equal(await who(page), 'View only', 'a stranger sees the console as view-only');

    await page.goto(`${ORIGIN}/signin`);
    await page.click('#login');
    await page.waitForFunction(() => document.getElementById('msg').textContent !== '');
    assert.match(await page.textContent('#msg'), /No passkey was used/, 'no passkey yet says so');
    assert.match(await page.textContent('#next'), /invite/, 'and names the way forward');

    // A fragment-only change would not reload the page's script.
    await page.goto('about:blank');
    await page.goto(`${ORIGIN}/signin#operator=owner&invite=wrong-${'x'.repeat(8)}`);
    await page.waitForFunction(() => location.hash === '');
    await page.click('#register');
    await page.waitForFunction(() => document.getElementById('msg').textContent !== '');
    assert.match(await page.textContent('#msg'), /invite is not valid/);
    assert.equal((await cdp.send('WebAuthn.getCredentials', { authenticatorId })).credentials.length, 0);

    // Whitespace around a pasted code is not part of the code.
    await page.goto('about:blank');
    await page.goto(`${ORIGIN}/signin#operator=owner&invite=${encodeURIComponent(` ${invite}\n`)}`);
    await page.click('#register');
    await reachConsole(page);
    assert.equal(await who(page), 'Owner', 'enrolling signs the operator in');
    assert.equal((await cdp.send('WebAuthn.getCredentials', { authenticatorId })).credentials.length, 1);

    await page.reload();
    assert.equal(await who(page), 'Owner', 'a reload keeps the session');

    // A browser restart keeps persistent cookies and drops session ones.
    const cookie = (await context.cookies()).find(c => c.name === '__Host-crew_session');
    assert.ok(cookie, 'a session cookie was set');
    assert.ok(cookie.expires > Date.now() / 1000 + 6 * 24 * 3600, 'the cookie outlives the browser session');
    assert.equal(cookie.secure && cookie.httpOnly, true);

    await page.goto(`${ORIGIN}/signin`);
    await page.waitForSelector('#who:not([hidden])');
    assert.match(await page.textContent('#who'), /Signed in as Owner/);
    assert.equal(await page.isHidden('#login'), true, 'a signed-in viewer is not asked to sign in');

    await page.click('#signout');
    await page.waitForSelector('#login:not([hidden])');
    const me = await page.evaluate(async () => (await fetch('/auth/me')).status);
    assert.equal(me, 401, 'sign-out ends the session');

    // The copied cookie is dead on the server, not just forgotten by the browser.
    const other = await browser.newContext({ ignoreHTTPSErrors: true });
    await other.addCookies([{ name: cookie.name, value: cookie.value, url: ORIGIN }]);
    const copy = await other.newPage();
    await copy.goto(`${ORIGIN}/healthz`);
    const replay = await copy.evaluate(async () => (await fetch('/auth/me')).status);
    assert.equal(replay, 401, 'a copied session cookie stops working after sign-out');
    await other.close();

    await page.click('#login');
    await reachConsole(page);
    assert.equal(await who(page), 'Owner', 'the saved passkey signs in again');
  } finally {
    await context.close();
  }
});

test('the invite is single-use', async () => {
  const { context, page } = await newSession();
  try {
    await page.goto(`${ORIGIN}/signin#operator=owner&invite=${encodeURIComponent(invite)}`);
    await page.click('#register');
    await page.waitForFunction(() => document.getElementById('msg').textContent !== '');
    assert.match(await page.textContent('#msg'), /invite was already used/);
  } finally {
    await context.close();
  }
});
