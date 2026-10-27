# Runbook: turn on Sign in with Xbox for crew-control

Audience: the owner. Agents prepare the chart and this page; they never create
the app registration, see the client secret, or run the commands marked
**owner terminal**. Passkey sign-in is unaffected at every step, so a mistake
here cannot lock anyone out.

Prerequisite: the chart plumbing (`crew.control.xbox` in
`jdw-deployments/charts/crew-lab`) is merged and rolled out, and crew-control
is at a version that has the Xbox sign-in (1.28.0 or later).

## What the code asks Microsoft for

- Endpoint: `https://login.microsoftonline.com/consumers/oauth2/v2.0/authorize`
  and `/token`. The `consumers` tenant means personal Microsoft accounts only.
- `response_type=code`, PKCE with `S256`, `response_mode=query`,
  `prompt=select_account`.
- `scope=XboxLive.signin` and nothing else. **`offline_access` is not
  requested**, so no refresh token exists. `openid`, `profile` and `User.Read`
  are not requested either.
- `redirect_uri` is `PUBLIC_ORIGIN` + `/auth/xbox/callback`, here
  `https://crew.prd.jdwlabs.com/auth/xbox/callback`. It must match the
  registration character for character.
- The access token is traded at Xbox Live for a user token, then an XSTS token;
  only the XUID and gamertag are read. No Microsoft token is stored or logged.

## 1. Entra app registration (portal, owner)

1. Sign in at <https://entra.microsoft.com> with the account that owns the
   tenant used for the other registrations. Go to **Entra ID > App
   registrations > New registration**.
2. **Name:** `crew-control-xbox-signin` (any name; players never see it as
   anything but the consent screen title).
3. **Supported account types:** choose **Personal Microsoft accounts only**.
   Not "Accounts in any organizational directory ... and personal Microsoft
   accounts"; the `consumers` endpoint rejects a registration that is not
   personal-only or multi-tenant-plus-personal, and personal-only is the
   narrowest.
4. **Redirect URI:** platform **Web**, value
   `https://crew.prd.jdwlabs.com/auth/xbox/callback`. Click **Register**.
5. On the **Overview** page copy **Application (client) ID**. It is a GUID and
   is not secret; it goes in `values.yaml` as `clientId`.
6. **Certificates & secrets > Client secrets > New client secret.** Description
   `crew-control`, expiry 24 months (the maximum). Copy the **Value** column
   immediately; it is shown once. The **Secret ID** column is not the secret.
   Put a reminder in your calendar a month before expiry.
7. **API permissions:** add nothing. `XboxLive.signin` is a scope on the Xbox
   Live resource that is requested at sign-in, not something this page grants;
   the default delegated `User.Read` entry is harmless and unused. Do not add
   `offline_access`.
8. **Authentication:** confirm the Web redirect URI is the only one and that
   **Allow public client flows** is **No**.

If the portal lists the app under "Supported account types" as anything other
than personal accounts, fix it in **Authentication > Supported accounts**
before continuing.

## 2. Find your XUID (no script, no credentials)

The XUID is the number Xbox gives an account; it is not secret and does not
change with the gamertag. The Bedrock server logs it when you join.

**Owner terminal**, after joining the lab server once with the Xbox account you
will sign in with:

```
kubectl -n jdwillmsen-prd logs statefulset/jdwillmsen-crew-lab-prd-minecraft-bedrock --since=24h \
  | grep "Player connected"
```

The line reads `Player connected: <gamertag>, xuid: 2535...`. Copy the digits.
The account must be the same Microsoft account you pick at the sign-in prompt.

Fallback if you cannot join: enable the flag with a placeholder (`xuid: "1"`),
try Sign in with Xbox once, and read the refusal, which names the XUID of the
account that tried:

```
kubectl -n jdwillmsen-prd logs deploy/crew-control --since=15m | grep xbox_login_refused
```

Then replace the placeholder and roll again.

## 3. Create the Secret (owner terminal, never an agent)

Write the secret value to a file with an editor so it never enters shell
history or a transcript. Paste only the value, nothing else.

```
umask 077
${EDITOR:-nano} ~/crew-xbox-client-secret.txt
kubectl -n jdwillmsen-prd create secret generic crew-control-xbox \
  --from-file=client-secret=$HOME/crew-xbox-client-secret.txt
shred -u ~/crew-xbox-client-secret.txt
```

The key must be `client-secret`; the chart mounts the Secret at `/xbox` and
crew-control reads `/xbox/client-secret`. The value needs at least 16
characters and no whitespace (a trailing newline is trimmed).

Check it exists without printing it:

```
kubectl -n jdwillmsen-prd get secret crew-control-xbox
```

## 4. Flip the values

In `jdw-deployments/charts/crew-lab/values.yaml`, under `crew.control.xbox`:

```yaml
    xbox:
      enabled: true
      clientId: "<Application (client) ID from step 1.5>"
      accounts:
        - operator: owner
          xuid: "<your XUID, quoted>"
```

Each account is an `operator` id from `crew.control.operators` and a quoted
XUID. Add one entry per person (`josh` has his own). Open a PR, merge it, and
let Argo CD roll crew-control. The pod restarts and nobody loses a session
except those who were signed in (they sign in again; passkeys still work).

If crew-control crash-loops, read `kubectl -n jdwillmsen-prd logs deploy/crew-control`.
The message names the variable at fault (client id not a GUID, secret file
missing or too short, an operator not in the list, an empty account list).
Fix or roll back (below).

## 5. Verify

1. `curl -s https://crew.prd.jdwlabs.com/auth/methods` returns
   `{"passkey":true,"xbox":true}`.
2. Open <https://crew.prd.jdwlabs.com/signin>: **Sign in with Xbox** appears
   beside the passkey button.
3. Click it, choose the Microsoft account, accept the consent screen. You land
   on the console signed in as your operator.
4. `kubectl -n jdwillmsen-prd logs deploy/crew-control | grep operator_login`
   shows `"method":"xbox"` with your XUID.
5. Sign in with a different Microsoft account (or ask someone) and confirm it
   returns to `/signin` with the "not allowed" sentence and no session.

## 6. Rollback

Set `enabled: false` under `crew.control.xbox` (leave the rest) and merge.
crew-control restarts with the Xbox routes unregistered, `/auth/methods` says
`"xbox":false`, the button disappears, and existing Xbox sessions end. Passkeys
and their sessions are untouched. The Secret and the app registration can stay;
delete the Secret with `kubectl -n jdwillmsen-prd delete secret crew-control-xbox`
and the client secret in the portal if you are abandoning it.

To revoke one person only, remove their entry from `accounts`; their session
ends at its next request.

## If Microsoft refuses

Whether Microsoft consents a **personal** app registration to `XboxLive.signin`
without Xbox partner enrolment was never verified without a real sign-in. It
may refuse. The sign-in fails closed and never creates a session. It presents
as:

- on Microsoft's page, before you come back: an error such as "invalid scope",
  `AADSTS70011`, or "this app is not allowed to use this permission"; or
- a return to `/signin?xbox=microsoft_error` or `exchange_failed`
  ("The Xbox sign-in did not work. Use your passkey."), with a log line
  `xbox_login_refused` whose `detail` holds Microsoft's error word and
  `AADSTS` number (`invalid_client` means a wrong or expired secret,
  `invalid_grant` a redirect URI mismatch or a replayed code, `invalid_scope`
  a scope refusal); or
- `xbox_refused` / `xbox_unavailable` when Microsoft accepts but Xbox Live does
  not (`child_account` and `no_xbox_profile` are about the account, not the
  app).

A refusal of the scope itself is the app registration's limit, not a bug to
retry: roll back (step 6) and file a follow-up.
