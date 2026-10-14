# Map Admin Access (Phase 1) Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Let operators of the FWB server be the map's admins, let them add a passkey from the game and log in with it without the game, and give agents an identity-free status of the production map, all switched off until the chart turns it on.

**Architecture:** The map reads the game's own operator list through the console bridge it already talks to, keeping a last-known-good copy on its volume. Passkeys are verified inside the map with `go-webauthn`, stored as public keys on the volume, and only made usable by a code the in-game agent whispers to the player. A new `gateAdmin` joins the existing route gates. A closed-shape status is served on the internal listener for agents, and on the public one for admins. The chart adds the switches, two alerts with promtool tests, and a Loki rule.

**Tech Stack:** Go 1.27 (one module at the repo root), `github.com/go-webauthn/webauthn` v0.18.2, Prometheus client, plain browser JavaScript tested with Node under `go test ./web/`, Playwright e2e harness, Helm, promtool, LogQL.

**Spec:** `minecraft/mcmap/docs/superpowers/specs/2026-10-14-map-admin-access-design.md` (approved at commit `899bcc3`). Read it beside this plan. Where the two disagree, the spec wins and the plan is wrong.

## Global Constraints

Copied from the spec. Every task's requirements include these.

- **No identity in git.** "No gamertag, XUID, name tag, real coordinate or seed in code, tests, fixtures, docs, commits or chart values. An owner's XUID in a chart value is not acceptable." Tests use the existing made-up player `Steve Builds` / `2535412345678901` and invented ones in the same style.
- **GitOps only.** "No `kubectl` changes to production."
- **No new secret from an agent or a person.** "nothing that mints, prints or exchanges a credential runs in an agent's shell." No step of this plan runs against production with a credential.
- **Phase 1 only.** No Dex (D4) and no agent session or agent-started checks (D9(b)).
- **An admin is an operator.** "Only the literal `operator` counts." The live list is cached for 10 seconds. The last-known-good set is used while `confirmedAt` is "under 24 hours old". `confirmedAt` is "rewritten at least once an hour while lookups succeed". A 404 from the bridge "falls back to last-known-good".
- **Passkey parameters.** The RP ID is "the host of `PUBLIC_ORIGIN`". The options set `residentKey: required`, `userVerification: required` and `attestation: none`, with ES256 and RS256 only. "`user.name` is `fwb-map` and `user.displayName` is `FWB map admin`." The user handle is "32 random bytes kept beside the XUID. The handle is never the XUID itself."
- **Registration limits.** "At most 5 per XUID." Labels are "1 to 32 letters, digits, spaces or hyphens", shown "as text only, never as HTML". There is "at most one pending credential per XUID, and a second registration while one is pending is refused, not replaced". The challenge, the pending credential and the code each last 2 minutes. "Three wrong codes delete the pending credential and its code."
- **Passkey sessions.** "A passkey session lasts 12 hours, not 7 days." The signed fields are `m: "passkey"` and `k`, "the first 16 bytes of the SHA-256 of the credential ID, base64url". The fingerprint is "the first 4 bytes of the SHA-256 of its ID as `XXXX-XXXX` in hex".
- **The login challenge is stateless.** It lives in a `__Host-mcmap_passkey` cookie (`HttpOnly`, `Secure`, `SameSite=Strict`), HMAC'd under a key derived with the label `mcmap passkey login challenge v1`. The used set takes "one atomic step" after the signature verifies and before anything is issued.
- **Logs.** New lines "do **not** carry a gamertag … a label, a coordinate, a challenge, a passkey code, a public key or a cookie". A credential is named by "the first 8 hex digits of the SHA-256 of its ID".
- **Metrics.** "Every label value in the table is declared at startup, at zero".
- **The CSP is never relaxed.** The header stays byte for byte what `secured` sets today.
- **The internal status has no free-text field.** Its fields are exactly those in the D8 tables.
- **Defaults.** `ADMIN_ENABLED` and `PASSKEYS_ENABLED` default to `false`. "Without `ADMIN_ENABLED` the map refuses to start" with passkeys on. With `AUTH_DISABLED=true`, nobody is admin and it "is never an error".
- **Dependency.** Pin `github.com/go-webauthn/webauthn` at `v0.18.2` exactly, in the root `go.mod`.
- **Never commit the build output** `minecraft/mcmap/mcmap`, which `.gitignore` already lists. **Stage by explicit path only:** never `git add -A` or `git add .`.
- **Comments say why, not what.** No ticket IDs, URLs or names in comments. Match the surrounding comment density and voice.
- **Commits.** Use conventional commits scoped to the component (`feat(mcmap): …`, `feat(agent): …`, `fix(bridge): …`, `feat(minecraft-fwb): …`). End each with the implementing agent's model in two trailers, for example:
  `Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>` and `Assisted-by: Claude Code:claude-opus-5-5`. A subagent on another model names its own model.
- **Go tests** run from the component's directory with stdin closed: `cd minecraft/<component> && go test -count=1 -race ./... </dev/null`. Page tests run by `go test ./web/` from `minecraft/mcmap`. Run `gofmt -l` and `go vet` before every commit, as CI's lint does.

## Review Focus

Five inputs the spec implies but its listed tests do not pin. Each has a test added to the task that owns the code.

1. **A label outside the allowed characters**, such as an accented letter, an emoji, or only spaces. A person expects a plain "1 to 32 letters, digits, spaces or hyphens" refusal before anything is created, never a 500 and never a passkey with a mangled name. Owner: Task 5, `TestRegistration_LabelIsCheckedBeforeAnythingIsMade`, and Task 10's page test.
2. **The map restarts between finishing a registration and confirming it.** Pending credentials are in memory. A person expects the agent to say "There is no passkey waiting…", the page to say start again, and the browser to be asked to drop the orphan. They should not see a hang or a credential that half-exists. Owner: Task 4, `TestPasskeys_PendingDoesNotSurviveAReload`, and Task 10.
3. **The confirm button pressed twice, or the request retried.** A person expects the passkey to be added once, with the second press answered "nothing waiting" and no error state. Owner: Task 4, `TestPasskeys_ConfirmTwiceAddsOnce`.
4. **An operator level spelled `Operator`, `OPERATOR` or with spaces in a hand-edited `permissions.json`.** A person reading the spec ("only the literal `operator`") expects that not to make an admin, and the agent agrees today. Owner: Task 2, the case-variants rows of `TestList_OnlyTheLiteralOperatorIsAnAdmin`.
5. **A garbage or oversized body sent to the public `POST /auth/passkey/finish`**: invalid JSON, bad base64, 1 MB. A person expects a 400 counted as `bad_assertion`, with the body read no further than a cap, never a 500 or a stalled request. Owner: Task 7, `TestPasskeyLogin_GarbageIsABadAssertionNotAFault`.

---

## How the work lands

Four groups. Each group is one pull request that stands alone and passes CI. Group 3's merge is the production rollout and is followed by rollout actions, which are listed with it.

| Group | Repository and branch | Tasks | Depends on |
|---|---|---|---|
| 1. Map release, switches off | `gameops`, branch `feat/map-admin-access-map` from `origin/main` | 1 to 11 | nothing |
| 2. Agent `!map passkey` | `gameops`, branch `feat/map-admin-access-agent` from `origin/main` | 12 to 13 | Group 1's `POST /internal/v1/passkey-codes` shape (Task 7). It can merge first: against an older map the endpoint answers 404, which the agent reports as "nothing waiting" |
| 3. Chart | `jdw-deployments`, branch `feat/map-admin-access` from `origin/main` | 14 to 16, then rollout Tasks 17 and 18 | Groups 1 and 2 released (it pins their image tags) |
| 4. Bridge logging | `gameops`, branch `fix/bridge-tellraw-logging` from `origin/main`; then a `jdw-deployments` image bump, `chore/console-bridge-tellraw-logging` | 19, then rollout Task 20 | nothing for the code. The bump restarts the game server, and nothing in phase 1 waits for it |

Create each `gameops` branch in its own worktree:

```bash
/usr/bin/git -C ~/projects/jdwillmsen/gameops fetch origin
/usr/bin/git -C ~/projects/jdwillmsen/gameops worktree add ~/worktrees/jdwillmsen/gameops/feat/map-admin-access-map -b feat/map-admin-access-map origin/main
```

Do the same for each other branch, with its own path. Never work on `main`.

## File structure

Group 1 (`minecraft/mcmap`):

| File | Responsibility |
|---|---|
| `internal/config/config.go` (modify) | `ADMIN_ENABLED`, `PASSKEYS_ENABLED`, `PUBLIC_ORIGIN` |
| `internal/operators/operators.go` (create) | The operator list: bridge lookup, 10 s cache, last-known-good file, hourly confirmation |
| `internal/operators/bridge.go` (create) | `GET /permissions` on the bridge |
| `internal/operators/metrics.go` (create) | `mcmap_admin_operators`, `mcmap_admin_operator_lookups_total` |
| `internal/auth/auth.go` (modify) | `m` and `k` claims, `IssuePasskey`, `PasskeyChecker`, `SessionFromContext` |
| `internal/auth/passkeys.go` (create) | The passkey store: pending, codes, active, counter, file |
| `internal/auth/ceremony.go` (create) | WebAuthn registration and login around `go-webauthn`, with the spec's own checks |
| `internal/auth/softkey_test.go` (create) | A software authenticator for tests |
| `internal/server/admin.go` (create) | `gateAdmin`, `/api/me`'s `admin`, the admin, player and internal passkey routes |
| `internal/server/status.go` (create) | The internal and admin status |
| `internal/server/admin_metrics.go` (create) | The D6 counters and gauges, declared at zero |
| `internal/server/server.go` (modify) | New fields, gate, routes |
| `internal/server/auth.go` (modify) | `/api/config`'s `passkeys`, revocation deletes passkeys, gamertag refresh |
| `cmd/mcmap/main.go` (modify) | Wiring |
| `web/passkey.js` (create), `web/index.html`, `web/style.css` (modify) | Passkey login, the Admin section, and the account panel |
| `web/testdata/passkey.test.cjs`, `web/passkey_run_test.go` (create) | Page tests |
| `e2e/harness/main.go`, `e2e/checks/access.mjs`, `e2e/lib/fields.mjs` (modify) | A fake operator list; a service session refused by admin routes |
| `README.md` (modify) | The "Admin access" section, the endpoints, metrics and variables |

Group 2 (`minecraft/agent`): `internal/mapclient/client.go`, `internal/plugins/maplogin.go`, `cmd/agent/main.go` (`speak`), and the tests beside them.

Group 3 (`jdw-deployments`): `charts/minecraft-fwb/values.yaml`, `templates/map.yaml`, `templates/map-admin-rules.yaml` (create), `templates/map-admin-loki-rule.yaml` (create), `templates/_helpers.tpl`, `tools/tests/test-map-admin.sh` (create), `charts/minecraft-fwb/README.md`.

Group 4 (`minecraft/bridge`): `http.go`, `http_test.go`.

---
## Group 1: map release with the switches off (`gameops`, `feat/map-admin-access-map`)

Every task in this group is in `minecraft/mcmap`. Run its tests from there:

```bash
cd minecraft/mcmap && go test -count=1 -race ./... </dev/null
```

### Task 1: Settings for admin and passkeys

**Files:**
- Modify: `minecraft/mcmap/internal/config/config.go` (the `Config` struct, after `ServiceSessionTTL`, and `Load` after the service-session block)
- Test: `minecraft/mcmap/internal/config/config_test.go`

**Interfaces:**
- Produces: `Config.Admin bool`, `Config.Passkeys bool`, `Config.PublicOrigin string` (exactly `https://<host>` with no path), `Config.PasskeyRPID string` (the host).

- [ ] **Step 1: Write the failing tests**

Append to `internal/config/config_test.go`:

```go
// Off unless asked for, and asked for in an order that makes sense: a
// passkey belongs to an admin, and is bound to one exact origin.
func TestLoad_AdminAndPasskeys(t *testing.T) {
	c, err := Load(with())
	if err != nil {
		t.Fatal(err)
	}
	if c.Admin || c.Passkeys || c.PublicOrigin != "" {
		t.Errorf("defaults: admin %v, passkeys %v, origin %q", c.Admin, c.Passkeys, c.PublicOrigin)
	}

	c, err = Load(with("ADMIN_ENABLED", "true", "PASSKEYS_ENABLED", "true", "PUBLIC_ORIGIN", "https://map.example"))
	if err != nil {
		t.Fatal(err)
	}
	if !c.Admin || !c.Passkeys || c.PublicOrigin != "https://map.example" || c.PasskeyRPID != "map.example" {
		t.Errorf("on: %+v", c)
	}

	for name, getenv := range map[string]func(string) string{
		"passkeys without admin":     with("PASSKEYS_ENABLED", "true", "PUBLIC_ORIGIN", "https://map.example"),
		"passkeys without an origin": with("ADMIN_ENABLED", "true", "PASSKEYS_ENABLED", "true"),
		"origin over plain http":     with("ADMIN_ENABLED", "true", "PASSKEYS_ENABLED", "true", "PUBLIC_ORIGIN", "http://map.example"),
		"origin with a path":         with("ADMIN_ENABLED", "true", "PASSKEYS_ENABLED", "true", "PUBLIC_ORIGIN", "https://map.example/map"),
		"origin with a port":         with("ADMIN_ENABLED", "true", "PASSKEYS_ENABLED", "true", "PUBLIC_ORIGIN", "https://map.example:8443"),
		"admin not a boolean":        with("ADMIN_ENABLED", "yes please"),
		"passkeys not a boolean":     with("PASSKEYS_ENABLED", "maybe"),
	} {
		if _, err := Load(getenv); err == nil {
			t.Errorf("%s: want an error", name)
		}
	}
}

// With no login there is no player to be an admin. That is a state to run
// in, never a reason to stop.
func TestLoad_AdminWithNoLoginIsNotAnError(t *testing.T) {
	bare := []string{"BRIDGE_URL", "http://bridge:8766", "BRIDGE_TOKEN", "tok", "LEVEL_NAME", "FWB", "AUTH_DISABLED", "true"}
	c, err := Load(env(append(bare, "ADMIN_ENABLED", "true", "PASSKEYS_ENABLED", "true", "PUBLIC_ORIGIN", "https://map.example")...))
	if err != nil {
		t.Fatalf("admin and passkeys with the login off: %v", err)
	}
	if c.Admin || c.Passkeys {
		t.Errorf("with no login, admin %v and passkeys %v, want both off", c.Admin, c.Passkeys)
	}
}
```

- [ ] **Step 2: Run the tests and confirm they fail**

Run: `cd minecraft/mcmap && go test -count=1 -run 'TestLoad_Admin' ./internal/config/ </dev/null`
Expected: FAIL to compile, `c.Admin undefined`.

- [ ] **Step 3: Implement**

In `Config`, after `ServiceSessionTTL`:

```go
	// Admin is whether the game's operators are the map's admins. Off, no
	// admin route is served and nobody is told they are one.
	Admin bool
	// Passkeys is whether an admin may add a passkey and log in with it.
	// It needs Admin, and PublicOrigin, the one origin a passkey is bound
	// to; PasskeyRPID is that origin's host.
	Passkeys     bool
	PublicOrigin string
	PasskeyRPID  string
```

In `Load`, after the service-session block:

```go
	if c.Admin, err = strconv.ParseBool(or(getenv("ADMIN_ENABLED"), "false")); err != nil {
		fail("ADMIN_ENABLED must be true or false")
	}
	if c.Passkeys, err = strconv.ParseBool(or(getenv("PASSKEYS_ENABLED"), "false")); err != nil {
		fail("PASSKEYS_ENABLED must be true or false")
	}
	if c.Passkeys {
		if !c.Admin {
			fail("PASSKEYS_ENABLED needs ADMIN_ENABLED: only an admin may add a passkey")
		}
		// A passkey is bound to the exact host it was made on, so anything
		// but a bare https origin would make passkeys nobody can use.
		raw := getenv("PUBLIC_ORIGIN")
		u, perr := url.Parse(raw)
		if perr != nil || u.Scheme != "https" || u.Host == "" || u.Port() != "" || (u.Path != "" && u.Path != "/") || u.RawQuery != "" || u.User != nil {
			fail("PUBLIC_ORIGIN must be the map's https origin with no port or path, got %q", raw)
		} else {
			c.PublicOrigin, c.PasskeyRPID = "https://"+u.Host, u.Hostname()
		}
	}
	// No login means no player, and so no admin: logged and run, not refused.
	if !c.Login {
		c.Admin, c.Passkeys = false, false
	}
```

- [ ] **Step 4: Run the tests and confirm they pass**

Run: `cd minecraft/mcmap && go test -count=1 -race ./internal/config/ </dev/null`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
gofmt -l minecraft/mcmap/internal/config && go vet ./minecraft/mcmap/internal/config/
/usr/bin/git add minecraft/mcmap/internal/config/config.go minecraft/mcmap/internal/config/config_test.go
/usr/bin/git commit -m "feat(mcmap): add the admin and passkey switches, off by default" -m "Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>
Assisted-by: Claude Code:claude-opus-5-5"
```

### Task 2: The operator list

**Files:**
- Create: `minecraft/mcmap/internal/operators/operators.go`, `bridge.go`, `metrics.go`
- Test: `minecraft/mcmap/internal/operators/operators_test.go`, `bridge_test.go`

**Interfaces:**
- Consumes: nothing from earlier tasks.
- Produces:
  ```go
  package operators
  type Source string
  const (Live Source = "live"; LastKnownGood Source = "last_known_good"; None Source = "none")
  type Lister interface { Permissions(ctx context.Context) (map[string]string, error) }
  func NewBridge(baseURL, token string) *Bridge          // implements Lister
  func Open(path string, l Lister, now func() time.Time, log *slog.Logger) (*List, error)
  func (o *List) IsOperator(ctx context.Context, xuid string) (bool, Source)
  func (o *List) Refresh(ctx context.Context) Source     // a lookup now, ignoring the cache
  func (o *List) Source() Source                         // where the last answer came from
  func (o *List) Run(ctx context.Context)                // startup lookup, then hourly
  const CacheFor = 10 * time.Second; const Window = 24 * time.Hour; const ConfirmEvery = time.Hour
  ```
  The file `operators.json` holds `{"xuids": [...], "confirmedAt": "<RFC 3339>"}` and nothing else.

- [ ] **Step 1: Write the failing tests**

`internal/operators/operators_test.go`:

```go
package operators

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus/testutil"
)

const steve, alex = "2535412345678901", "2535412345678902"

type clock struct {
	mu sync.Mutex
	t  time.Time
}

func (c *clock) now() time.Time      { c.mu.Lock(); defer c.mu.Unlock(); return c.t }
func (c *clock) add(d time.Duration) { c.mu.Lock(); c.t = c.t.Add(d); c.mu.Unlock() }

type fakeBridge struct {
	mu    sync.Mutex
	perms map[string]string
	err   error
	asked int
}

func (f *fakeBridge) Permissions(context.Context) (map[string]string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.asked++
	return f.perms, f.err
}

func (f *fakeBridge) set(perms map[string]string, err error) {
	f.mu.Lock()
	f.perms, f.err = perms, err
	f.mu.Unlock()
}

func quiet() *slog.Logger { return slog.New(slog.NewTextHandler(io.Discard, nil)) }

func open(t *testing.T, b *fakeBridge, c *clock) (*List, string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "operators.json")
	l, err := Open(path, b, c.now, quiet())
	if err != nil {
		t.Fatal(err)
	}
	return l, path
}

func TestList_OnlyTheLiteralOperatorIsAnAdmin(t *testing.T) {
	c := &clock{t: time.Unix(1_790_000_000, 0)}
	for level, want := range map[string]bool{
		"operator": true, "member": false, "visitor": false, "": false,
		"Operator": false, "OPERATOR": false, " operator": false, "operator ": false, "admin": false,
	} {
		b := &fakeBridge{perms: map[string]string{steve: level}}
		l, _ := open(t, b, c)
		if got, src := l.IsOperator(t.Context(), steve); got != want || src != Live {
			t.Errorf("level %q: admin %v from %s, want %v from live", level, got, src, want)
		}
	}
	b := &fakeBridge{perms: map[string]string{steve: "operator"}}
	l, _ := open(t, b, c)
	if got, _ := l.IsOperator(t.Context(), alex); got {
		t.Error("an XUID absent from the list is an admin")
	}
}

func TestList_AsksTheBridgeAtMostOnceInTenSeconds(t *testing.T) {
	c := &clock{t: time.Unix(1_790_000_000, 0)}
	b := &fakeBridge{perms: map[string]string{steve: "operator"}}
	l, _ := open(t, b, c)
	for range 5 {
		l.IsOperator(t.Context(), steve)
	}
	if b.asked != 1 {
		t.Fatalf("asked %d times within the cache, want 1", b.asked)
	}
	b.set(map[string]string{steve: "member"}, nil)
	c.add(CacheFor)
	if got, _ := l.IsOperator(t.Context(), steve); got {
		t.Error("a demotion is not seen once the cache has run out")
	}
}

func TestList_FallsBackToLastKnownGoodForADayFromConfirmation(t *testing.T) {
	c := &clock{t: time.Unix(1_790_000_000, 0)}
	b := &fakeBridge{perms: map[string]string{steve: "operator"}}
	l, path := open(t, b, c)
	l.Refresh(t.Context())

	for name, failure := range map[string]error{"unreachable": errors.New("dial tcp: refused"), "file deleted": ErrNoFile} {
		b.set(nil, failure)
		c.add(CacheFor)
		if got, src := l.IsOperator(t.Context(), steve); !got || src != LastKnownGood {
			t.Errorf("%s within the window: admin %v from %s", name, got, src)
		}
	}
	// A list unchanged for days is still confirmed within the hour while
	// lookups succeed, so the window runs from the last success.
	b.set(map[string]string{steve: "operator"}, nil)
	for range 30 {
		c.add(ConfirmEvery)
		l.Refresh(t.Context())
	}
	b.set(nil, errors.New("bridge down"))
	c.add(23 * time.Hour)
	if got, src := l.IsOperator(t.Context(), steve); !got || src != LastKnownGood {
		t.Errorf("an unchanged list confirmed an hour before the outage, 23 h in: admin %v from %s", got, src)
	}
	c.add(2 * time.Hour)
	if got, src := l.IsOperator(t.Context(), steve); got || src != None {
		t.Errorf("past the window: admin %v from %s, want nobody", got, src)
	}

	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), "member") || strings.Contains(string(raw), "gamertag") || !strings.Contains(string(raw), steve) {
		t.Errorf("the file holds more than the operators' XUIDs and the time: %s", raw)
	}
}

func TestList_RewritesTheSetOnlyWhenItChangesAndTheTimeHourly(t *testing.T) {
	c := &clock{t: time.Unix(1_790_000_000, 0)}
	b := &fakeBridge{perms: map[string]string{steve: "operator"}}
	l, path := open(t, b, c)
	l.Refresh(t.Context())
	first, _ := os.Stat(path)

	c.add(10 * time.Minute)
	l.Refresh(t.Context())
	if again, _ := os.Stat(path); !again.ModTime().Equal(first.ModTime()) {
		t.Error("an unchanged set was rewritten within the hour")
	}
	c.add(ConfirmEvery)
	l.Refresh(t.Context())
	if read(t, path).ConfirmedAt.Before(c.now().Add(-time.Minute)) {
		t.Error("confirmedAt was not moved on after an hour of successful lookups")
	}
	b.set(map[string]string{steve: "operator", alex: "operator"}, nil)
	l.Refresh(t.Context())
	if got := read(t, path).XUIDs; len(got) != 2 {
		t.Errorf("a changed set was not written: %v", got)
	}
}

func TestList_StartupLookupSetsTheGauge(t *testing.T) {
	c := &clock{t: time.Unix(1_790_000_000, 0)}
	b := &fakeBridge{perms: map[string]string{steve: "operator", alex: "member"}}
	l, path := open(t, b, c)
	l.Refresh(t.Context())
	if got := testutil.ToFloat64(metricOperators); got != 1 {
		t.Errorf("gauge after a live lookup = %v, want 1", got)
	}

	// The map restarts with the bridge down: the gauge comes from the file.
	b.set(nil, errors.New("down"))
	again, err := Open(path, b, c.now, quiet())
	if err != nil {
		t.Fatal(err)
	}
	if src := again.Refresh(t.Context()); src != LastKnownGood || testutil.ToFloat64(metricOperators) != 1 {
		t.Errorf("restart with the bridge down: source %s, gauge %v", src, testutil.ToFloat64(metricOperators))
	}
	c.add(Window + time.Minute)
	if src := again.Refresh(t.Context()); src != None || testutil.ToFloat64(metricOperators) != 0 {
		t.Errorf("past the window: source %s, gauge %v", src, testutil.ToFloat64(metricOperators))
	}
}

func TestList_EveryLookupResultIsDeclared(t *testing.T) {
	for _, src := range []Source{Live, LastKnownGood, None} {
		if _, err := metricLookups.GetMetricWithLabelValues(string(src)); err != nil {
			t.Errorf("%s: %v", src, err)
		}
	}
}

func read(t *testing.T, path string) file {
	t.Helper()
	f, err := load(path)
	if err != nil {
		t.Fatal(err)
	}
	return f
}
```

`internal/operators/bridge_test.go`:

```go
package operators

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestBridge_AsksWithTheTokenAndReadsTheMap(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/permissions" || r.Header.Get("Authorization") != "Bearer tok" {
			http.Error(w, "no", http.StatusUnauthorized)
			return
		}
		_, _ = w.Write([]byte(`{"` + steve + `":"operator","` + alex + `":"member"}`))
	}))
	defer srv.Close()
	got, err := NewBridge(srv.URL+"/", "tok").Permissions(t.Context())
	if err != nil || got[steve] != "operator" || got[alex] != "member" {
		t.Fatalf("Permissions = %v, %v", got, err)
	}
}

// A deleted file is an outage to ride out on the last-known-good list, not
// a list with nobody on it.
func TestBridge_AMissingFileIsAnErrorNotAnEmptyList(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "permissions.json not found", http.StatusNotFound)
	}))
	defer srv.Close()
	got, err := NewBridge(srv.URL, "tok").Permissions(t.Context())
	if !errors.Is(err, ErrNoFile) || got != nil {
		t.Fatalf("Permissions = %v, %v; want ErrNoFile", got, err)
	}
}
```

- [ ] **Step 2: Run the tests and confirm they fail**

Run: `cd minecraft/mcmap && go test -count=1 ./internal/operators/ </dev/null`
Expected: FAIL to compile, `undefined: Open`.

- [ ] **Step 3: Implement**

`internal/operators/bridge.go`:

```go
package operators

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// ErrNoFile is the bridge saying permissions.json is not there. It is an
// error and not an empty list: a file deleted by hand must not make every
// admin vanish, nor be how one is removed.
var ErrNoFile = errors.New("operators: permissions.json is not there")

// Bridge reads the game's own operator list from the console bridge.
type Bridge struct {
	URL, Token string
	HTTP       *http.Client
}

func NewBridge(baseURL, token string) *Bridge {
	return &Bridge{URL: strings.TrimRight(baseURL, "/"), Token: token, HTTP: &http.Client{
		Timeout:       5 * time.Second,
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}}
}

func (b *Bridge) Permissions(ctx context.Context) (map[string]string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, b.URL+"/permissions", nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+b.Token)
	resp, err := b.HTTP.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	switch resp.StatusCode {
	case http.StatusOK:
	case http.StatusNotFound:
		return nil, ErrNoFile
	default:
		return nil, fmt.Errorf("operators: bridge answered %d", resp.StatusCode)
	}
	var perms map[string]string
	if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&perms); err != nil {
		return nil, fmt.Errorf("operators: %w", err)
	}
	return perms, nil
}
```

`internal/operators/metrics.go`:

```go
package operators

import (
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
)

var (
	metricOperators = promauto.NewGauge(prometheus.GaugeOpts{
		Name: "mcmap_admin_operators",
		Help: "How many operators the map knows of: from the live list, or from the last-known-good one while it is under a day old. A count, never who.",
	})
	metricLookups = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "mcmap_admin_operator_lookups_total",
		Help: "Lookups of the operator list, by where the answer came from: live, last_known_good while the bridge could not be read, or none.",
	}, []string{"result"})
)

func init() {
	for _, s := range []Source{Live, LastKnownGood, None} {
		metricLookups.WithLabelValues(string(s))
	}
}
```

`internal/operators/operators.go`:

```go
// Package operators says who the map's admins are: the players the game's
// own permissions.json makes operators, read through the console bridge.
package operators

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"os"
	"path/filepath"
	"slices"
	"sync"
	"time"
)

type Source string

const (
	Live          Source = "live"
	LastKnownGood Source = "last_known_good"
	None          Source = "none"
)

const (
	CacheFor     = 10 * time.Second
	Window       = 24 * time.Hour
	ConfirmEvery = time.Hour
)

type Lister interface {
	Permissions(ctx context.Context) (map[string]string, error)
}

type file struct {
	XUIDs       []string  `json:"xuids"`
	ConfirmedAt time.Time `json:"confirmedAt"`
}

// List is the operator list with two fallbacks. While the bridge cannot be
// read, the last set read is used for a day from when it was last
// confirmed, so that an outage of the bridge does not also lock the owner
// out of the map he would debug it with. The cost is that a demotion
// waits for the bridge.
type List struct {
	path   string
	lister Lister
	now    func() time.Time
	log    *slog.Logger

	mu        sync.Mutex
	live      map[string]bool
	askedAt   time.Time
	source    Source
	kept      file
	failLog   time.Time
}

func Open(path string, l Lister, now func() time.Time, log *slog.Logger) (*List, error) {
	o := &List{path: path, lister: l, now: now, log: log, source: None}
	kept, err := load(path)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}
	o.kept = kept
	return o, nil
}

func load(path string) (file, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return file{}, err
	}
	var f file
	return f, json.Unmarshal(raw, &f)
}

func (o *List) IsOperator(ctx context.Context, xuid string) (bool, Source) {
	o.mu.Lock()
	fresh := o.live != nil && o.now().Sub(o.askedAt) < CacheFor
	o.mu.Unlock()
	if !fresh {
		o.Refresh(ctx)
	}
	o.mu.Lock()
	defer o.mu.Unlock()
	switch o.source {
	case Live:
		return o.live[xuid], Live
	case LastKnownGood:
		return slices.Contains(o.kept.XUIDs, xuid), LastKnownGood
	}
	return false, None
}

func (o *List) Source() Source {
	o.mu.Lock()
	defer o.mu.Unlock()
	return o.source
}

func (o *List) Refresh(ctx context.Context) Source {
	perms, err := o.lister.Permissions(ctx)
	o.mu.Lock()
	defer o.mu.Unlock()
	now := o.now()
	o.askedAt = now
	if err != nil {
		o.live = map[string]bool{}
		if now.Sub(o.failLog) >= time.Minute {
			o.failLog = now
			o.log.Warn("map operator lookup failed", "error_class", classOf(err))
		}
		if !o.kept.ConfirmedAt.IsZero() && now.Sub(o.kept.ConfirmedAt) < Window {
			o.source = LastKnownGood
			metricOperators.Set(float64(len(o.kept.XUIDs)))
		} else {
			o.source = None
			metricOperators.Set(0)
		}
		metricLookups.WithLabelValues(string(o.source)).Inc()
		return o.source
	}
	live, xuids := map[string]bool{}, []string{}
	for xuid, level := range perms {
		if level == "operator" {
			live[xuid] = true
			xuids = append(xuids, xuid)
		}
	}
	slices.Sort(xuids)
	o.live, o.source = live, Live
	metricOperators.Set(float64(len(xuids)))
	metricLookups.WithLabelValues(string(Live)).Inc()
	if !slices.Equal(xuids, o.kept.XUIDs) || now.Sub(o.kept.ConfirmedAt) >= ConfirmEvery {
		next := file{XUIDs: xuids, ConfirmedAt: now.UTC()}
		if err := writeWhole(o.path, next); err != nil {
			o.log.Error("map operator list not kept", "error_class", "write")
		} else {
			o.kept = next
		}
	}
	return Live
}

// Run looks the list up now and then hourly, so that the gauge and the
// confirmation time stay current while nobody is using the map.
func (o *List) Run(ctx context.Context) {
	o.Refresh(ctx)
	tick := time.NewTicker(ConfirmEvery)
	defer tick.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
			o.Refresh(ctx)
		}
	}
}

// classOf says what kind of failure it was and nothing a caller wrote.
func classOf(err error) string {
	switch {
	case errors.Is(err, ErrNoFile):
		return "no_file"
	case errors.Is(err, context.DeadlineExceeded):
		return "timeout"
	}
	return "unreachable"
}

func writeWhole(path string, f file) error {
	raw, err := json.Marshal(f)
	if err != nil {
		return err
	}
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, ".tmp-*")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	if _, err := tmp.Write(raw); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), path)
}
```

- [ ] **Step 4: Run the tests and confirm they pass**

Run: `cd minecraft/mcmap && go test -count=1 -race ./internal/operators/ </dev/null`
Expected: PASS. If `TestList_StartupLookupSetsTheGauge` sees the gauge from another test, that is because the gauge is package-global. The test sets it itself before reading, so it does not depend on order.

- [ ] **Step 5: Commit**

```bash
gofmt -l minecraft/mcmap/internal/operators && go vet ./minecraft/mcmap/internal/operators/
/usr/bin/git add minecraft/mcmap/internal/operators/operators.go minecraft/mcmap/internal/operators/bridge.go minecraft/mcmap/internal/operators/metrics.go minecraft/mcmap/internal/operators/operators_test.go minecraft/mcmap/internal/operators/bridge_test.go
/usr/bin/git commit -m "feat(mcmap): read the game's operators, with a day of last-known-good" -m "Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>
Assisted-by: Claude Code:claude-opus-5-5"
```

### Task 3: Passkey sessions in the cookie

**Files:**
- Modify: `minecraft/mcmap/internal/auth/auth.go` (`Sessions`, `claims`, `verify`, plus a new accessor)
- Test: `minecraft/mcmap/internal/auth/passkey_session_test.go` (create)

**Interfaces:**
- Produces:
  ```go
  const MethodPasskey = "passkey"
  type PasskeyChecker interface { Active(key, xuid string) bool }
  // fields on Sessions:
  Passkeys   PasskeyChecker // nil refuses every passkey session
  PasskeyTTL time.Duration
  func (s *Sessions) IssuePasskey(w http.ResponseWriter, id Identity, key string) time.Time
  type Session struct { Identity; IssuedMilli int64; Method string }
  func SessionFromContext(ctx context.Context) (Session, bool) // false for a service session
  ```

- [ ] **Step 1: Write the failing tests**

`internal/auth/passkey_session_test.go`:

```go
package auth

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

type activeKeys map[string]string // key to XUID

func (a activeKeys) Active(key, xuid string) bool { return a[key] == xuid }

func passkeyCookie(t *testing.T, s *Sessions, key string) *http.Cookie {
	t.Helper()
	rec := httptest.NewRecorder()
	s.IssuePasskey(rec, steve, key)
	return rec.Result().Cookies()[0]
}

func TestPasskeySession_LastsTwelveHoursAndNamesItsKey(t *testing.T) {
	c := &clock{time.Unix(1_790_000_000, 0)}
	s := newSessions(t, c)
	s.Passkeys, s.PasskeyTTL = activeKeys{"key-1": steve.XUID}, 12*time.Hour
	cookie := passkeyCookie(t, s, "key-1")
	if cookie.MaxAge != int((12 * time.Hour).Seconds()) {
		t.Errorf("MaxAge = %d", cookie.MaxAge)
	}
	if got, ok := verify(s, cookie); !ok || got != steve {
		t.Fatalf("Verify = %+v, %v", got, ok)
	}
	c.t = c.t.Add(12 * time.Hour)
	if _, ok := verify(s, cookie); ok {
		t.Error("a passkey session outlived twelve hours")
	}
}

// Removing a passkey, or switching passkeys off, ends its sessions on the
// next request and not when they would have run out.
func TestPasskeySession_EndsWithItsKeyOrTheSwitch(t *testing.T) {
	c := &clock{time.Unix(1_790_000_000, 0)}
	s := newSessions(t, c)
	keys := activeKeys{"key-1": steve.XUID}
	s.Passkeys, s.PasskeyTTL = keys, 12*time.Hour
	cookie := passkeyCookie(t, s, "key-1")

	delete(keys, "key-1")
	if _, ok := verify(s, cookie); ok {
		t.Error("a removed passkey's session still verifies")
	}
	keys["key-1"] = steve.XUID
	s.Passkeys = nil
	if _, ok := verify(s, cookie); ok {
		t.Error("a passkey session verifies with passkeys switched off")
	}
	// A key that names another player is no key of this one's.
	s.Passkeys = activeKeys{"key-1": "2535412345678902"}
	if _, ok := verify(s, cookie); ok {
		t.Error("a passkey session verifies against another player's key")
	}
}

func TestPasskeySession_CookiesThatCannotBeOursAreRefused(t *testing.T) {
	c := &clock{time.Unix(1_790_000_000, 0)}
	s := newSessions(t, c)
	s.Passkeys, s.PasskeyTTL = activeKeys{"key-1": steve.XUID}, 12*time.Hour
	now := c.t
	for name, cl := range map[string]claims{
		"unknown method":       {Identity: steve, IssuedMilli: now.UnixMilli(), Expires: now.Add(time.Hour).Unix(), Method: "dex"},
		"passkey with no key":  {Identity: steve, IssuedMilli: now.UnixMilli(), Expires: now.Add(time.Hour).Unix(), Method: MethodPasskey},
		"game code with a key": {Identity: steve, IssuedMilli: now.UnixMilli(), Expires: now.Add(time.Hour).Unix(), Key: "key-1"},
	} {
		if _, ok := verify(s, forge(s.Key, cl)); ok {
			t.Errorf("%s: verified", name)
		}
	}
	// A game-code session made before any of this verifies as it did.
	old := issue(t, s, steve)
	if _, ok := verify(s, old); !ok {
		t.Error("a game-code session no longer verifies")
	}
}

func TestSessionFromContext_SaysHowAndWhen(t *testing.T) {
	c := &clock{time.Unix(1_790_000_000, 0)}
	s := newSessions(t, c)
	s.Passkeys, s.PasskeyTTL = activeKeys{"key-1": steve.XUID}, 12*time.Hour
	var got Session
	h := s.Require(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) { got, _ = SessionFromContext(r.Context()) }))
	req := httptest.NewRequest("GET", "/", nil)
	req.AddCookie(passkeyCookie(t, s, "key-1"))
	h.ServeHTTP(httptest.NewRecorder(), req)
	if got.Identity != steve || got.Method != MethodPasskey || got.IssuedMilli != c.t.UnixMilli() {
		t.Errorf("SessionFromContext = %+v", got)
	}
}
```

`forge` already exists in `service_test.go` in this package: it signs a `claims` with a key.

- [ ] **Step 2: Run the tests and confirm they fail**

Run: `cd minecraft/mcmap && go test -count=1 -run 'TestPasskeySession|TestSessionFromContext' ./internal/auth/ </dev/null`
Expected: FAIL to compile, `s.IssuePasskey undefined`.

- [ ] **Step 3: Implement**

In `auth.go`, add to `claims`:

```go
	// Method is how the session was had: absent for a game code, "passkey"
	// for a passkey. Key names that passkey, as the first 16 bytes of the
	// SHA-256 of its ID, so that removing it can end its sessions.
	Method string `json:"m,omitempty"`
	Key    string `json:"k,omitempty"`
```

Add to `Sessions`:

```go
	// Passkeys is asked about every passkey session, on every request.
	// Nil refuses them all, which is what switching passkeys off does to
	// sessions already issued.
	Passkeys   PasskeyChecker
	PasskeyTTL time.Duration
```

New declarations:

```go
const MethodPasskey = "passkey"

type PasskeyChecker interface {
	Active(key, xuid string) bool
}

// IssuePasskey logs the browser in as id by way of the passkey key names.
func (s *Sessions) IssuePasskey(w http.ResponseWriter, id Identity, key string) time.Time {
	return s.issue(w, s.Key, s.PasskeyTTL, claims{Identity: id, Method: MethodPasskey, Key: key})
}

// Session is what a handler may know of the login it was let through on.
type Session struct {
	Identity
	IssuedMilli int64
	Method      string
}

// SessionFromContext answers false for a service session, as FromContext
// does.
func SessionFromContext(ctx context.Context) (Session, bool) {
	c, ok := ctx.Value(contextKey{}).(claims)
	if !ok || c.Service || !IsXUID(c.XUID) {
		return Session{}, false
	}
	return Session{Identity: c.Identity, IssuedMilli: c.IssuedMilli, Method: c.Method}, true
}
```

In `verify`, after the `if c.XUID == ""` check and before the revocation check:

```go
	switch c.Method {
	case "":
		if c.Key != "" {
			return claims{}, false
		}
	case MethodPasskey:
		if c.Key == "" || s.Passkeys == nil || !s.Passkeys.Active(c.Key, c.XUID) {
			return claims{}, false
		}
	default:
		return claims{}, false
	}
```

- [ ] **Step 4: Run the whole package's tests and confirm they pass**

Run: `cd minecraft/mcmap && go test -count=1 -race ./internal/auth/ </dev/null`
Expected: PASS, including every existing session test.

- [ ] **Step 5: Commit**

```bash
gofmt -l minecraft/mcmap/internal/auth && go vet ./minecraft/mcmap/internal/auth/
/usr/bin/git add minecraft/mcmap/internal/auth/auth.go minecraft/mcmap/internal/auth/passkey_session_test.go
/usr/bin/git commit -m "feat(mcmap): carry a passkey session's key in the cookie and check it every request" -m "Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>
Assisted-by: Claude Code:claude-opus-5-5"
```

### Task 4: The passkey store

**Files:**
- Create: `minecraft/mcmap/internal/auth/passkeys.go`
- Test: `minecraft/mcmap/internal/auth/passkeys_test.go`

**Interfaces:**
- Consumes: `writeWhole`, `random`, `randomBytes` and `encoding` (already in package `auth`).
- Produces:
  ```go
  type Passkey struct {
      ID, PublicKey, UserHandle []byte; SignCount uint32
      BackupEligible, BackupState bool
      XUID, Gamertag, Label string; Created, LastUsed time.Time
  }
  var ErrPendingExists, ErrTooMany, ErrDuplicate, ErrNothingPending, ErrWrongCode, ErrAbandoned, ErrCounter, ErrUnknownKey error
  func LoadPasskeys(path string, now func() time.Time) (*Passkeys, error)
  func KeyOf(credentialID []byte) string        // base64url of SHA-256[:16]
  func Fingerprint(credentialID []byte) string  // "7F3A-91C2"
  func LogName(credentialID []byte) string      // 8 hex digits, for logs
  func (p *Passkeys) Active(key, xuid string) bool          // implements PasskeyChecker
  func (p *Passkeys) UserHandle(xuid string) []byte          // stable per XUID, random on first use
  func (p *Passkeys) Existing(xuid string) []Passkey         // active ones, for excludeCredentials
  func (p *Passkeys) StartPending(pk Passkey, issuedMilli int64) error
  func (p *Passkeys) Cancel(xuid string, issuedMilli int64) (Passkey, bool)
  func (p *Passkeys) IssueCode(xuid string) (code, fingerprint string, err error)
  func (p *Passkeys) Confirm(xuid string, issuedMilli int64, credentialID []byte, code string) (Passkey, int, error) // int: tries left after a wrong code
  func (p *Passkeys) ByHandle(handle []byte) (xuid string, keys []Passkey, ok bool)
  func (p *Passkeys) IsPending(credentialID []byte) bool
  func (p *Passkeys) HasPending(xuid string) bool
  func (p *Passkeys) Lookup(handle, credentialID []byte) (xuid string, k Passkey, pending bool, ok bool)
  func (p *Passkeys) Use(credentialID []byte, signCount uint32, backupEligible, backupState bool) (Passkey, error)
  func (p *Passkeys) List(xuid string) []Listed   // Listed{Key, Label string; Created, LastUsed time.Time; Pending bool}
  func (p *Passkeys) Remove(xuid, key string) (Passkey, bool)
  func (p *Passkeys) RemoveAll(xuid string) []Passkey
  func (p *Passkeys) RefreshGamertag(xuid, gamertag string)
  func (p *Passkeys) Counts() (pending, active int)
  Counted func(pending, active int)   // field: told after every change
  Expired func()                      // field: told once for each pending credential that runs out
  const PendingFor = 2 * time.Minute; const MaxPerPlayer = 5; const MaxWrongCodes = 3
  ```
  Pending credentials and codes live in memory only. Active ones are in `passkeys.json`.

- [ ] **Step 1: Write the failing tests**

`internal/auth/passkeys_test.go`:

```go
package auth

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

func newPasskeys(t *testing.T, c *clock) (*Passkeys, string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "passkeys.json")
	p, err := LoadPasskeys(path, c.now)
	if err != nil {
		t.Fatal(err)
	}
	return p, path
}

func key(id string, xuid string) Passkey {
	return Passkey{ID: []byte(id), PublicKey: []byte("pk-" + id), XUID: xuid, Gamertag: "Steve Builds", Label: "Phone"}
}

const issued = int64(1_790_000_000_000)

func confirmOne(t *testing.T, p *Passkeys, id string) Passkey {
	t.Helper()
	if err := p.StartPending(key(id, steve.XUID), issued); err != nil {
		t.Fatal(err)
	}
	code, _, err := p.IssueCode(steve.XUID)
	if err != nil {
		t.Fatal(err)
	}
	got, _, err := p.Confirm(steve.XUID, issued, []byte(id), code)
	if err != nil {
		t.Fatal(err)
	}
	return got
}

func TestPasskeys_PendingIsUselessUntilTheWhisperedCodeIsTyped(t *testing.T) {
	c := &clock{time.Unix(1_790_000_000, 0)}
	p, _ := newPasskeys(t, c)
	if err := p.StartPending(key("cred-1", steve.XUID), issued); err != nil {
		t.Fatal(err)
	}
	if p.Active(KeyOf([]byte("cred-1")), steve.XUID) || !p.IsPending([]byte("cred-1")) {
		t.Fatal("a pending credential is active")
	}
	code, fp, err := p.IssueCode(steve.XUID)
	if err != nil || len(code) != 6 || fp != Fingerprint([]byte("cred-1")) {
		t.Fatalf("IssueCode = %q %q %v", code, fp, err)
	}
	if _, _, err := p.Confirm(steve.XUID, issued, []byte("cred-1"), strings.ToLower(code)); err != nil {
		t.Fatalf("a right code typed in lower case: %v", err)
	}
	if !p.Active(KeyOf([]byte("cred-1")), steve.XUID) {
		t.Error("a confirmed credential is not active")
	}
}

func TestPasskeys_ASecondRegistrationWhileOneIsPendingIsRefused(t *testing.T) {
	c := &clock{time.Unix(1_790_000_000, 0)}
	p, _ := newPasskeys(t, c)
	if err := p.StartPending(key("cred-a", steve.XUID), issued); err != nil {
		t.Fatal(err)
	}
	if err := p.StartPending(key("cred-b", steve.XUID), issued); !errors.Is(err, ErrPendingExists) {
		t.Fatalf("second registration: %v, want ErrPendingExists", err)
	}
	// Only the same session may cancel; then a new one may start.
	if _, ok := p.Cancel(steve.XUID, issued+1); ok {
		t.Error("another session cancelled the pending credential")
	}
	if _, ok := p.Cancel(steve.XUID, issued); !ok {
		t.Fatal("the same session could not cancel")
	}
	if err := p.StartPending(key("cred-b", steve.XUID), issued); err != nil {
		t.Fatal(err)
	}
	c.t = c.t.Add(PendingFor)
	if err := p.StartPending(key("cred-c", steve.XUID), issued); err != nil {
		t.Errorf("after the pending one expired: %v", err)
	}
}

// Two browsers on one stolen cookie: the attacker's page cannot have its
// credential activated by the owner's page, which confirms its own.
func TestPasskeys_TheReplacementRace(t *testing.T) {
	c := &clock{time.Unix(1_790_000_000, 0)}
	p, _ := newPasskeys(t, c)
	_ = p.StartPending(key("owners", steve.XUID), issued)
	if err := p.StartPending(key("attackers", steve.XUID), issued); !errors.Is(err, ErrPendingExists) {
		t.Fatalf("the attacker replaced the pending credential: %v", err)
	}
	p.Cancel(steve.XUID, issued)
	_ = p.StartPending(key("attackers", steve.XUID), issued)
	code, fp, _ := p.IssueCode(steve.XUID)
	if fp == Fingerprint([]byte("owners")) {
		t.Fatal("the whisper shows the owner's fingerprint for the attacker's credential")
	}
	if _, _, err := p.Confirm(steve.XUID, issued, []byte("owners"), code); !errors.Is(err, ErrWrongCode) {
		t.Fatalf("the owner's page confirmed with the attacker's code: %v", err)
	}
	if p.Active(KeyOf([]byte("attackers")), steve.XUID) {
		t.Error("the attacker's credential was activated by the owner's page")
	}
}

func TestPasskeys_ThreeWrongCodesAbandonTheRegistration(t *testing.T) {
	c := &clock{time.Unix(1_790_000_000, 0)}
	p, _ := newPasskeys(t, c)
	_ = p.StartPending(key("cred-1", steve.XUID), issued)
	code, _, _ := p.IssueCode(steve.XUID)
	for want := 2; want >= 1; want-- {
		if _, left, err := p.Confirm(steve.XUID, issued, []byte("cred-1"), "ZZZZZZ"); !errors.Is(err, ErrWrongCode) || left != want {
			t.Fatalf("wrong code: left %d err %v, want %d", left, err, want)
		}
	}
	if _, _, err := p.Confirm(steve.XUID, issued, []byte("cred-1"), "ZZZZZZ"); !errors.Is(err, ErrAbandoned) {
		t.Fatalf("third wrong code: %v, want ErrAbandoned", err)
	}
	if _, _, err := p.Confirm(steve.XUID, issued, []byte("cred-1"), code); !errors.Is(err, ErrNothingPending) {
		t.Fatalf("the right code after abandonment: %v", err)
	}
}

func TestPasskeys_ConfirmIsRefusedOutsideItsSessionAndTime(t *testing.T) {
	c := &clock{time.Unix(1_790_000_000, 0)}
	p, _ := newPasskeys(t, c)
	_ = p.StartPending(key("cred-1", steve.XUID), issued)
	if _, _, err := p.Confirm(steve.XUID, issued, []byte("cred-1"), "ABC234"); !errors.Is(err, ErrWrongCode) {
		t.Errorf("before any code was issued: %v", err)
	}
	old, _, _ := p.IssueCode(steve.XUID)
	code, _, _ := p.IssueCode(steve.XUID)
	if old == code {
		t.Fatal("a second code is the same as the first")
	}
	if _, _, err := p.Confirm(steve.XUID, issued, []byte("cred-1"), old); !errors.Is(err, ErrWrongCode) {
		t.Errorf("a replaced code: %v", err)
	}
	if _, _, err := p.Confirm(steve.XUID, issued+1, []byte("cred-1"), code); !errors.Is(err, ErrNothingPending) {
		t.Errorf("another session's IssuedMilli: %v", err)
	}
	if _, _, err := p.Confirm("2535412345678902", issued, []byte("cred-1"), code); !errors.Is(err, ErrNothingPending) {
		t.Errorf("another XUID: %v", err)
	}
	c.t = c.t.Add(PendingFor)
	if _, _, err := p.Confirm(steve.XUID, issued, []byte("cred-1"), code); !errors.Is(err, ErrNothingPending) {
		t.Errorf("after two minutes: %v", err)
	}
	if _, _, err := p.IssueCode(steve.XUID); !errors.Is(err, ErrNothingPending) {
		t.Errorf("a code for an expired registration: %v", err)
	}
}

// Review focus: a retried confirm adds the passkey once.
func TestPasskeys_ConfirmTwiceAddsOnce(t *testing.T) {
	c := &clock{time.Unix(1_790_000_000, 0)}
	p, _ := newPasskeys(t, c)
	_ = p.StartPending(key("cred-1", steve.XUID), issued)
	code, _, _ := p.IssueCode(steve.XUID)
	if _, _, err := p.Confirm(steve.XUID, issued, []byte("cred-1"), code); err != nil {
		t.Fatal(err)
	}
	if _, _, err := p.Confirm(steve.XUID, issued, []byte("cred-1"), code); !errors.Is(err, ErrNothingPending) {
		t.Errorf("second confirm: %v", err)
	}
	if _, active := p.Counts(); active != 1 {
		t.Errorf("%d active, want 1", active)
	}
}

// Review focus: pending credentials are in memory, and a restart drops them.
func TestPasskeys_PendingDoesNotSurviveAReload(t *testing.T) {
	c := &clock{time.Unix(1_790_000_000, 0)}
	p, path := newPasskeys(t, c)
	confirmOne(t, p, "kept")
	_ = p.StartPending(key("half-made", steve.XUID), issued)
	again, err := LoadPasskeys(path, c.now)
	if err != nil {
		t.Fatal(err)
	}
	if !again.Active(KeyOf([]byte("kept")), steve.XUID) {
		t.Error("an active passkey did not survive a reload")
	}
	if again.IsPending([]byte("half-made")) {
		t.Error("a pending credential survived a reload")
	}
	if _, _, err := again.IssueCode(steve.XUID); !errors.Is(err, ErrNothingPending) {
		t.Errorf("a code after a reload: %v", err)
	}
}

func TestPasskeys_AtMostFiveAndNoDuplicates(t *testing.T) {
	c := &clock{time.Unix(1_790_000_000, 0)}
	p, _ := newPasskeys(t, c)
	for i := range MaxPerPlayer {
		confirmOne(t, p, "cred-"+string(rune('a'+i)))
	}
	if err := p.StartPending(key("cred-z", steve.XUID), issued); !errors.Is(err, ErrTooMany) {
		t.Errorf("a sixth: %v", err)
	}
	if err := p.StartPending(key("cred-a", "2535412345678902"), issued); !errors.Is(err, ErrDuplicate) {
		t.Errorf("a credential ID already held: %v", err)
	}
}

func TestPasskeys_CounterRules(t *testing.T) {
	c := &clock{time.Unix(1_790_000_000, 0)}
	p, _ := newPasskeys(t, c)
	confirmOne(t, p, "synced")
	if _, err := p.Use([]byte("synced"), 0, false, false); err != nil {
		t.Errorf("both counters zero: %v", err)
	}
	if _, err := p.Use([]byte("synced"), 5, false, false); err != nil {
		t.Errorf("a higher count: %v", err)
	}
	for _, n := range []uint32{5, 4, 0} {
		if _, err := p.Use([]byte("synced"), n, false, false); !errors.Is(err, ErrCounter) {
			t.Errorf("count %d after 5: %v, want ErrCounter", n, err)
		}
	}
	if _, err := p.Use([]byte("synced"), 9, true, false); !errors.Is(err, ErrCounter) {
		t.Errorf("BE changed since registration: %v", err)
	}
}

func TestPasskeys_ConcurrentUsesOfOneCountPassOnce(t *testing.T) {
	c := &clock{time.Unix(1_790_000_000, 0)}
	p, _ := newPasskeys(t, c)
	confirmOne(t, p, "key")
	var wg sync.WaitGroup
	var mu sync.Mutex
	passed := 0
	for range 20 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, err := p.Use([]byte("key"), 7, false, false); err == nil {
				mu.Lock()
				passed++
				mu.Unlock()
			}
		}()
	}
	wg.Wait()
	if passed != 1 {
		t.Errorf("%d of 20 uses with the same count passed, want 1", passed)
	}
}

func TestPasskeys_RemoveAllIsOnePlayersOnly(t *testing.T) {
	c := &clock{time.Unix(1_790_000_000, 0)}
	p, path := newPasskeys(t, c)
	confirmOne(t, p, "steves")
	alex := key("alexs", "2535412345678902")
	_ = p.StartPending(alex, issued)
	code, _, _ := p.IssueCode(alex.XUID)
	_, _, _ = p.Confirm(alex.XUID, issued, alex.ID, code)
	_ = p.StartPending(key("steves-pending", steve.XUID), issued)

	if gone := p.RemoveAll(steve.XUID); len(gone) != 2 {
		t.Errorf("removed %d of Steve's, want the active and the pending one", len(gone))
	}
	if !p.Active(KeyOf(alex.ID), alex.XUID) {
		t.Error("another player's passkey was removed")
	}
	raw, _ := os.ReadFile(path)
	if strings.Contains(string(raw), steve.XUID) {
		t.Error("the removal is not on disk")
	}
}

func TestPasskeys_NamesAreStableAndShort(t *testing.T) {
	id := []byte("credential-id")
	if fp := Fingerprint(id); len(fp) != 9 || fp[4] != '-' || strings.ToUpper(fp) != fp {
		t.Errorf("Fingerprint = %q", fp)
	}
	if len(LogName(id)) != 8 || KeyOf(id) == KeyOf([]byte("other")) {
		t.Errorf("LogName %q, KeyOf %q", LogName(id), KeyOf(id))
	}
}

func TestPasskeys_TheUserHandleIsRandomAndKept(t *testing.T) {
	c := &clock{time.Unix(1_790_000_000, 0)}
	p, _ := newPasskeys(t, c)
	h := p.UserHandle(steve.XUID)
	if len(h) != 32 || strings.Contains(string(h), steve.XUID) {
		t.Fatalf("handle %x", h)
	}
	k := key("cred-1", steve.XUID)
	k.UserHandle = h
	_ = p.StartPending(k, issued)
	code, _, _ := p.IssueCode(steve.XUID)
	_, _, _ = p.Confirm(steve.XUID, issued, k.ID, code)
	if string(p.UserHandle(steve.XUID)) != string(h) {
		t.Error("a player's handle changed once they had a passkey")
	}
	if xuid, keys, ok := p.ByHandle(h); !ok || xuid != steve.XUID || len(keys) != 1 {
		t.Errorf("ByHandle = %q %d %v", xuid, len(keys), ok)
	}
	if _, _, _, ok := p.Lookup(p.UserHandle("2535412345678902"), k.ID); ok {
		t.Error("a credential was found under another player's handle")
	}
	if xuid, _, pend, ok := p.Lookup(h, k.ID); !ok || pend || xuid != steve.XUID {
		t.Errorf("Lookup = %q pending %v ok %v", xuid, pend, ok)
	}
}
```

- [ ] **Step 2: Run the tests and confirm they fail**

Run: `cd minecraft/mcmap && go test -count=1 -run TestPasskeys ./internal/auth/ </dev/null`
Expected: FAIL to compile, `undefined: LoadPasskeys`.

- [ ] **Step 3: Implement**

`internal/auth/passkeys.go`:

```go
package auth

import (
	"bytes"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"
	"sync"
	"time"
)

const (
	PendingFor    = 2 * time.Minute
	MaxPerPlayer  = 5
	MaxWrongCodes = 3
)

var (
	ErrPendingExists  = errors.New("a passkey is already waiting to be confirmed")
	ErrTooMany        = errors.New("that player holds as many passkeys as are allowed")
	ErrDuplicate      = errors.New("that credential is already registered")
	ErrNothingPending = errors.New("there is no passkey waiting to be confirmed")
	ErrWrongCode      = errors.New("that code is not the one sent")
	ErrAbandoned      = errors.New("too many wrong codes; start again")
	ErrCounter        = errors.New("the authenticator's counter did not move forward")
	ErrUnknownKey     = errors.New("no such passkey")
)

type Passkey struct {
	ID             []byte    `json:"id"`
	PublicKey      []byte    `json:"publicKey"`
	UserHandle     []byte    `json:"userHandle"`
	SignCount      uint32    `json:"signCount"`
	BackupEligible bool      `json:"backupEligible"`
	BackupState    bool      `json:"backupState"`
	XUID           string    `json:"xuid"`
	Gamertag       string    `json:"gamertag"`
	Label          string    `json:"label"`
	Created        time.Time `json:"created"`
	LastUsed       time.Time `json:"lastUsed"`
}

type Listed struct {
	Key      string    `json:"id"`
	Label    string    `json:"label"`
	Created  time.Time `json:"created"`
	LastUsed time.Time `json:"lastUsed,omitzero"`
	Pending  bool      `json:"pending,omitempty"`
}

type pending struct {
	key         Passkey
	issuedMilli int64
	expires     time.Time
	code        string
	wrong       int
}

// Passkeys holds every passkey the map knows. Active ones are on disk;
// pending ones, and their codes, only in memory, so a restart drops a
// registration half made and the page starts it again.
type Passkeys struct {
	path string
	now  func() time.Time
	// Counted is told how many of each kind there are, after every change.
	Counted func(pending, active int)
	// Expired is told of each pending credential that ran out unconfirmed.
	Expired func()

	mu      sync.Mutex
	active  map[string]*Passkey // by KeyOf(ID)
	handles map[string][]byte   // XUID to user handle
	waiting map[string]*pending // by XUID
}

func LoadPasskeys(path string, now func() time.Time) (*Passkeys, error) {
	p := &Passkeys{path: path, now: now, active: map[string]*Passkey{}, handles: map[string][]byte{}, waiting: map[string]*pending{}}
	raw, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return p, nil
	}
	if err != nil {
		return nil, err
	}
	var list []Passkey
	if err := json.Unmarshal(raw, &list); err != nil {
		return nil, fmt.Errorf("passkeys %s: %w", path, err)
	}
	for i := range list {
		k := list[i]
		p.active[KeyOf(k.ID)] = &k
		p.handles[k.XUID] = k.UserHandle
	}
	return p, nil
}

func sum(id []byte) [sha256.Size]byte { return sha256.Sum256(id) }

func KeyOf(id []byte) string { s := sum(id); return encoding.EncodeToString(s[:16]) }

func Fingerprint(id []byte) string {
	s := sum(id)
	h := strings.ToUpper(hex.EncodeToString(s[:4]))
	return h[:4] + "-" + h[4:]
}

func LogName(id []byte) string { s := sum(id); return hex.EncodeToString(s[:4]) }

func (p *Passkeys) Active(key, xuid string) bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	k, ok := p.active[key]
	return ok && k.XUID == xuid
}

func (p *Passkeys) UserHandle(xuid string) []byte {
	p.mu.Lock()
	defer p.mu.Unlock()
	if h, ok := p.handles[xuid]; ok {
		return h
	}
	h := randomBytes(32)
	p.handles[xuid] = h
	return h
}

func (p *Passkeys) Existing(xuid string) []Passkey {
	p.mu.Lock()
	defer p.mu.Unlock()
	var out []Passkey
	for _, k := range p.active {
		if k.XUID == xuid {
			out = append(out, *k)
		}
	}
	return out
}

// prune drops pending registrations past their two minutes. Called with
// the lock held.
func (p *Passkeys) prune() {
	now := p.now()
	gone := false
	for xuid, w := range p.waiting {
		if !now.Before(w.expires) {
			delete(p.waiting, xuid)
			gone = true
			if p.Expired != nil {
				p.Expired()
			}
		}
	}
	if gone {
		p.counted()
	}
}

func (p *Passkeys) StartPending(k Passkey, issuedMilli int64) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.prune()
	if _, ok := p.waiting[k.XUID]; ok {
		return ErrPendingExists
	}
	if _, held := p.active[KeyOf(k.ID)]; held {
		return ErrDuplicate
	}
	for _, w := range p.waiting {
		if bytes.Equal(w.key.ID, k.ID) {
			return ErrDuplicate
		}
	}
	n := 0
	for _, a := range p.active {
		if a.XUID == k.XUID {
			n++
		}
	}
	if n >= MaxPerPlayer {
		return ErrTooMany
	}
	k.Created = p.now().UTC()
	p.waiting[k.XUID] = &pending{key: k, issuedMilli: issuedMilli, expires: p.now().Add(PendingFor)}
	p.counted()
	return nil
}

func (p *Passkeys) Cancel(xuid string, issuedMilli int64) (Passkey, bool) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.prune()
	w, ok := p.waiting[xuid]
	if !ok || w.issuedMilli != issuedMilli {
		return Passkey{}, false
	}
	delete(p.waiting, xuid)
	p.counted()
	return w.key, true
}

func (p *Passkeys) IssueCode(xuid string) (code, fingerprint string, err error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.prune()
	w, ok := p.waiting[xuid]
	if !ok {
		return "", "", ErrNothingPending
	}
	for code = random(codeLength); code == w.code; code = random(codeLength) {
	}
	w.code = code
	return code, Fingerprint(w.key.ID), nil
}

// Confirm activates the pending credential only for the session that made
// it, the credential that session's page created, and the code sent for
// that credential.
func (p *Passkeys) Confirm(xuid string, issuedMilli int64, credentialID []byte, code string) (Passkey, int, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.prune()
	w, ok := p.waiting[xuid]
	if !ok || w.issuedMilli != issuedMilli {
		return Passkey{}, 0, ErrNothingPending
	}
	right := w.code != "" && bytes.Equal(w.key.ID, credentialID) &&
		subtle.ConstantTimeCompare([]byte(strings.ToUpper(strings.TrimSpace(code))), []byte(w.code)) == 1
	if !right {
		w.wrong++
		if w.wrong >= MaxWrongCodes {
			delete(p.waiting, xuid)
			p.counted()
			return Passkey{}, 0, ErrAbandoned
		}
		return Passkey{}, MaxWrongCodes - w.wrong, ErrWrongCode
	}
	delete(p.waiting, xuid)
	k := w.key
	p.active[KeyOf(k.ID)] = &k
	p.handles[xuid] = k.UserHandle
	if err := p.save(); err != nil {
		delete(p.active, KeyOf(k.ID))
		return Passkey{}, 0, err
	}
	p.counted()
	return k, 0, nil
}

func (p *Passkeys) IsPending(id []byte) bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.prune()
	for _, w := range p.waiting {
		if bytes.Equal(w.key.ID, id) {
			return true
		}
	}
	return false
}

func (p *Passkeys) HasPending(xuid string) bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.prune()
	_, ok := p.waiting[xuid]
	return ok
}

// Lookup finds the credential an assertion names, active or pending, among
// the credentials of the player whose handle it carries and no other's.
func (p *Passkeys) Lookup(handle, id []byte) (string, Passkey, bool, bool) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.prune()
	for xuid, h := range p.handles {
		if subtle.ConstantTimeCompare(h, handle) != 1 {
			continue
		}
		if k, ok := p.active[KeyOf(id)]; ok && k.XUID == xuid {
			return xuid, *k, false, true
		}
		if w, ok := p.waiting[xuid]; ok && bytes.Equal(w.key.ID, id) {
			return xuid, w.key, true, true
		}
		return "", Passkey{}, false, false
	}
	return "", Passkey{}, false, false
}

func (p *Passkeys) ByHandle(handle []byte) (string, []Passkey, bool) {
	p.mu.Lock()
	defer p.mu.Unlock()
	for xuid, h := range p.handles {
		if subtle.ConstantTimeCompare(h, handle) != 1 {
			continue
		}
		var keys []Passkey
		for _, k := range p.active {
			if k.XUID == xuid {
				keys = append(keys, *k)
			}
		}
		return xuid, keys, len(keys) > 0
	}
	return "", nil, false
}

// Use records a login. Reading, judging and writing the count are one step
// under the lock, so two logins with one count cannot both pass.
func (p *Passkeys) Use(id []byte, signCount uint32, backupEligible, backupState bool) (Passkey, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	k, ok := p.active[KeyOf(id)]
	if !ok {
		return Passkey{}, ErrUnknownKey
	}
	if backupEligible != k.BackupEligible {
		return Passkey{}, ErrCounter
	}
	if (signCount != 0 || k.SignCount != 0) && signCount <= k.SignCount {
		return Passkey{}, ErrCounter
	}
	before := *k
	k.SignCount, k.BackupState, k.LastUsed = signCount, backupState, p.now().UTC()
	if err := p.save(); err != nil {
		*k = before
		return Passkey{}, err
	}
	return *k, nil
}

func (p *Passkeys) List(xuid string) []Listed {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.prune()
	var out []Listed
	for key, k := range p.active {
		if k.XUID == xuid {
			out = append(out, Listed{Key: key, Label: k.Label, Created: k.Created, LastUsed: k.LastUsed})
		}
	}
	if w, ok := p.waiting[xuid]; ok {
		out = append(out, Listed{Key: KeyOf(w.key.ID), Label: w.key.Label, Created: w.key.Created, Pending: true})
	}
	return out
}

func (p *Passkeys) Remove(xuid, key string) (Passkey, bool) {
	p.mu.Lock()
	defer p.mu.Unlock()
	k, ok := p.active[key]
	if !ok || k.XUID != xuid {
		return Passkey{}, false
	}
	delete(p.active, key)
	if err := p.save(); err != nil {
		p.active[key] = k
		return Passkey{}, false
	}
	p.counted()
	return *k, true
}

func (p *Passkeys) RemoveAll(xuid string) []Passkey {
	p.mu.Lock()
	defer p.mu.Unlock()
	var gone []Passkey
	for key, k := range p.active {
		if k.XUID == xuid {
			gone = append(gone, *k)
			delete(p.active, key)
		}
	}
	if w, ok := p.waiting[xuid]; ok {
		gone = append(gone, w.key)
		delete(p.waiting, xuid)
	}
	_ = p.save()
	p.counted()
	return gone
}

func (p *Passkeys) RefreshGamertag(xuid, gamertag string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	changed := false
	for _, k := range p.active {
		if k.XUID == xuid && k.Gamertag != gamertag {
			k.Gamertag, changed = gamertag, true
		}
	}
	if changed {
		_ = p.save()
	}
}

func (p *Passkeys) Counts() (int, int) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.prune()
	return len(p.waiting), len(p.active)
}

func (p *Passkeys) counted() {
	if p.Counted != nil {
		p.Counted(len(p.waiting), len(p.active))
	}
}

func (p *Passkeys) save() error {
	list := make([]Passkey, 0, len(p.active))
	for _, k := range p.active {
		list = append(list, *k)
	}
	raw, err := json.Marshal(list)
	if err != nil {
		return err
	}
	return writeWhole(p.path, raw)
}
```

`codeLength` and `random` come from `codes.go`, so a passkey code uses the login codes' alphabet.

- [ ] **Step 4: Run the tests and confirm they pass**

Run: `cd minecraft/mcmap && go test -count=1 -race ./internal/auth/ </dev/null`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
gofmt -l minecraft/mcmap/internal/auth && go vet ./minecraft/mcmap/internal/auth/
/usr/bin/git add minecraft/mcmap/internal/auth/passkeys.go minecraft/mcmap/internal/auth/passkeys_test.go
/usr/bin/git commit -m "feat(mcmap): keep passkeys, pending until a code from the game confirms them" -m "Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>
Assisted-by: Claude Code:claude-opus-5-5"
```

### Task 5: The WebAuthn ceremonies

**Files:**
- Modify: `go.mod`, `go.sum` (repo root)
- Create: `minecraft/mcmap/internal/auth/ceremony.go`
- Test: `minecraft/mcmap/internal/auth/softkey_test.go`, `minecraft/mcmap/internal/auth/ceremony_test.go`

**Interfaces:**
- Consumes: `Passkeys` with `UserHandle`, `Existing`, `HasPending`, `StartPending`, `Lookup` and `Use` (Task 4), and `Identity` (existing).
- Produces:
  ```go
  const PasskeyChallengeCookie = cookiePrefix + "mcmap_passkey"
  var ErrLabel, ErrRegistrationExpired, ErrRegistrationRefused error
  type CeremonyError struct{ Result string }   // Result is one of the Result* constants
  const (ResultBadAssertion = "bad_assertion"; ResultUnknownCredential = "unknown_credential"; ResultExpired = "expired"; ResultPending = "pending"; ResultCounter = "counter"; ResultReplayed = "replayed")
  func NewCeremonies(origin, rpID string, sessionKey []byte, keys *Passkeys, now func() time.Time) (*Ceremonies, error)
  func (c *Ceremonies) BeginRegistration(xuid string, issuedMilli int64, label string) (*protocol.CredentialCreation, error)
  func (c *Ceremonies) FinishRegistration(id Identity, issuedMilli int64, body []byte) (Passkey, error)
  func (c *Ceremonies) BeginLogin(w http.ResponseWriter) (*protocol.CredentialAssertion, error)
  func (c *Ceremonies) FinishLogin(w http.ResponseWriter, r *http.Request, body []byte) (Passkey, error) // a *CeremonyError on refusal
  func PasskeyChallengeKey(sessionKey []byte) []byte  // HMAC(sessionKey, "mcmap passkey login challenge v1\x00")
  ```

- [ ] **Step 1: Add the dependency, pinned**

```bash
cd ~/worktrees/jdwillmsen/gameops/feat/map-admin-access-map
go get github.com/go-webauthn/webauthn@v0.18.2
go mod tidy
grep -n 'go-webauthn/webauthn v0.18.2' go.mod
```

Expected: one line `github.com/go-webauthn/webauthn v0.18.2` in the `require` block. If `go mod tidy` moves any other module's version, stop and find out why before going on.

- [ ] **Step 2: Write the software authenticator the tests use**

`internal/auth/softkey_test.go`:

```go
package auth

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"testing"

	"github.com/go-webauthn/webauthn/protocol/webauthncbor"
)

// softKey is an authenticator in the test: an ES256 key that answers
// create() and get() the way a browser hands them to the page, so that the
// map's checks are run against real signatures and real CBOR.
type softKey struct {
	t     *testing.T
	priv  *ecdsa.PrivateKey
	id    []byte
	count uint32
	// flags beyond UP and UV, which every ceremony sets unless told not to.
	be, bs bool
}

const (
	flagUP = 0x01
	flagUV = 0x04
	flagBE = 0x08
	flagBS = 0x10
	flagAT = 0x40
)

func newSoftKey(t *testing.T) *softKey {
	t.Helper()
	priv, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	id := make([]byte, 32)
	_, _ = rand.Read(id)
	return &softKey{t: t, priv: priv, id: id}
}

var b64 = base64.RawURLEncoding

// ceremony is what a test may change about one answer.
type ceremony struct {
	rpID, origin, challenge, typ string
	clientData                   func(map[string]any)
	flags                        byte // replaces UP|UV when non-zero
	format                       string
	alg                          int
	id                           []byte
}

func (k *softKey) flagsFor(c ceremony) byte {
	f := byte(flagUP | flagUV)
	if c.flags != 0 {
		f = c.flags
	}
	if k.be {
		f |= flagBE
	}
	if k.bs {
		f |= flagBS
	}
	return f
}

func (k *softKey) clientDataJSON(c ceremony) []byte {
	cd := map[string]any{"type": c.typ, "challenge": c.challenge, "origin": c.origin, "crossOrigin": false}
	if c.clientData != nil {
		c.clientData(cd)
	}
	raw, _ := json.Marshal(cd)
	return raw
}

func (k *softKey) cose(alg int) []byte {
	x, y := k.priv.PublicKey.X.FillBytes(make([]byte, 32)), k.priv.PublicKey.Y.FillBytes(make([]byte, 32))
	raw, err := webauthncbor.Marshal(map[int]any{1: 2, 3: alg, -1: 1, -2: x, -3: y})
	if err != nil {
		k.t.Fatal(err)
	}
	return raw
}

func authData(rpID string, flags byte, count uint32, attested []byte) []byte {
	h := sha256.Sum256([]byte(rpID))
	out := append(h[:], flags)
	out = binary.BigEndian.AppendUint32(out, count)
	return append(out, attested...)
}

// create answers navigator.credentials.create() as the page would send it.
func (k *softKey) create(c ceremony) []byte {
	if c.typ == "" {
		c.typ = "webauthn.create"
	}
	if c.format == "" {
		c.format = "none"
	}
	if c.alg == 0 {
		c.alg = -7
	}
	id := k.id
	if c.id != nil {
		id = c.id
	}
	attested := make([]byte, 16) // AAGUID, all zero for "none"
	attested = binary.BigEndian.AppendUint16(attested, uint16(len(id)))
	attested = append(attested, id...)
	attested = append(attested, k.cose(c.alg)...)
	ad := authData(c.rpID, k.flagsFor(c)|flagAT, k.count, attested)
	att, err := webauthncbor.Marshal(map[string]any{"fmt": c.format, "attStmt": map[string]any{}, "authData": ad})
	if err != nil {
		k.t.Fatal(err)
	}
	body, _ := json.Marshal(map[string]any{
		"id": b64.EncodeToString(id), "rawId": b64.EncodeToString(id), "type": "public-key",
		"response": map[string]any{"clientDataJSON": b64.EncodeToString(k.clientDataJSON(c)), "attestationObject": b64.EncodeToString(att)},
	})
	return body
}

// get answers navigator.credentials.get() for a discoverable login.
func (k *softKey) get(c ceremony, userHandle []byte) []byte {
	if c.typ == "" {
		c.typ = "webauthn.get"
	}
	k.count++
	ad := authData(c.rpID, k.flagsFor(c), k.count, nil)
	cdj := k.clientDataJSON(c)
	h := sha256.Sum256(cdj)
	digest := sha256.Sum256(append(append([]byte{}, ad...), h[:]...))
	sig, err := ecdsa.SignASN1(rand.Reader, k.priv, digest[:])
	if err != nil {
		k.t.Fatal(err)
	}
	resp := map[string]any{"clientDataJSON": b64.EncodeToString(cdj), "authenticatorData": b64.EncodeToString(ad), "signature": b64.EncodeToString(sig)}
	if userHandle != nil {
		resp["userHandle"] = b64.EncodeToString(userHandle)
	}
	body, _ := json.Marshal(map[string]any{"id": b64.EncodeToString(k.id), "rawId": b64.EncodeToString(k.id), "type": "public-key", "response": resp})
	return body
}
```

- [ ] **Step 3: Write the failing ceremony tests**

`internal/auth/ceremony_test.go`:

```go
package auth

import (
	"bytes"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

const (
	origin = "https://map.example"
	rpID   = "map.example"
)

func ceremonies(t *testing.T, c *clock) (*Ceremonies, *Passkeys) {
	t.Helper()
	keys, _ := newPasskeys(t, c)
	cer, err := NewCeremonies(origin, rpID, []byte(strings.Repeat("k", 32)), keys, c.now)
	if err != nil {
		t.Fatal(err)
	}
	return cer, keys
}

func challengeOf(t *testing.T, options any) string {
	t.Helper()
	raw, _ := json.Marshal(options)
	var o struct {
		PublicKey struct {
			Challenge string `json:"challenge"`
		} `json:"publicKey"`
	}
	if err := json.Unmarshal(raw, &o); err != nil || o.PublicKey.Challenge == "" {
		t.Fatalf("options without a challenge: %s", raw)
	}
	return o.PublicKey.Challenge
}

// register runs a whole registration and confirmation; it is the fixture
// every login test starts from.
func register(t *testing.T, cer *Ceremonies, keys *Passkeys, k *softKey) {
	t.Helper()
	opts, err := cer.BeginRegistration(steve.XUID, issued, "Phone")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := cer.FinishRegistration(steve, issued, k.create(ceremony{rpID: rpID, origin: origin, challenge: challengeOf(t, opts)})); err != nil {
		t.Fatal(err)
	}
	code, _, _ := keys.IssueCode(steve.XUID)
	if _, _, err := keys.Confirm(steve.XUID, issued, k.id, code); err != nil {
		t.Fatal(err)
	}
}

func TestRegistration_OptionsAreTheSpecsAndNameNobody(t *testing.T) {
	c := &clock{time.Unix(1_790_000_000, 0)}
	cer, _ := ceremonies(t, c)
	opts, err := cer.BeginRegistration(steve.XUID, issued, "Phone")
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(opts)
	for _, want := range []string{`"residentKey":"required"`, `"userVerification":"required"`, `"attestation":"none"`, `"alg":-7`, `"alg":-257`, `"name":"fwb-map"`, `"displayName":"FWB map admin"`, `"id":"map.example"`} {
		if !bytes.Contains(raw, []byte(want)) {
			t.Errorf("options lack %s: %s", want, raw)
		}
	}
	for _, never := range []string{steve.XUID, steve.Gamertag} {
		if bytes.Contains(raw, []byte(never)) {
			t.Errorf("options name the player (%q): %s", never, raw)
		}
	}
}

// Review focus: a label is judged before anything is made.
func TestRegistration_LabelIsCheckedBeforeAnythingIsMade(t *testing.T) {
	c := &clock{time.Unix(1_790_000_000, 0)}
	cer, keys := ceremonies(t, c)
	for _, label := range []string{"", "   ", "Café", "Phone 📱", strings.Repeat("a", 33), "<b>x</b>", "tab\there"} {
		if _, err := cer.BeginRegistration(steve.XUID, issued, label); !errors.Is(err, ErrLabel) {
			t.Errorf("label %q: %v, want ErrLabel", label, err)
		}
	}
	if keys.HasPending(steve.XUID) {
		t.Error("a refused label left something pending")
	}
	for _, label := range []string{"Phone", "Work laptop 2", "Key-A"} {
		if _, err := cer.BeginRegistration(steve.XUID, issued, label); err != nil {
			t.Errorf("label %q: %v", label, err)
		}
	}
}

func TestRegistration_EveryCheckOfTheSpec(t *testing.T) {
	c := &clock{time.Unix(1_790_000_000, 0)}
	for name, tc := range map[string]struct {
		mutate  func(*ceremony)
		session func() (Identity, int64)
		key     func(*softKey)
	}{
		"wrong type":         {mutate: func(c *ceremony) { c.typ = "webauthn.get" }},
		"wrong origin":       {mutate: func(c *ceremony) { c.origin = "https://elsewhere.example" }},
		"cross origin":       {mutate: func(c *ceremony) { c.clientData = func(m map[string]any) { m["crossOrigin"] = true } }},
		"top origin present": {mutate: func(c *ceremony) { c.clientData = func(m map[string]any) { m["topOrigin"] = origin } }},
		"wrong rp id":        {mutate: func(c *ceremony) { c.rpID = "elsewhere.example" }},
		"wrong challenge":    {mutate: func(c *ceremony) { c.challenge = b64.EncodeToString(bytes.Repeat([]byte{7}, 32)) }},
		"no user presence":   {mutate: func(c *ceremony) { c.flags = flagUV }},
		"no verification":    {mutate: func(c *ceremony) { c.flags = flagUP }},
		"backed up, not eligible": {key: func(k *softKey) { k.bs = true }},
		"another alg":        {mutate: func(c *ceremony) { c.alg = -8 }},
		"packed attestation": {mutate: func(c *ceremony) { c.format = "packed" }},
		"id over 1023 bytes": {mutate: func(c *ceremony) { c.id = bytes.Repeat([]byte{1}, 1024) }},
		"another session":    {session: func() (Identity, int64) { return steve, issued + 1 }},
		"another player":     {session: func() (Identity, int64) { return Identity{XUID: "2535412345678902", Gamertag: "Alex"}, issued }},
	} {
		t.Run(name, func(t *testing.T) {
			cer, keys := ceremonies(t, c)
			opts, err := cer.BeginRegistration(steve.XUID, issued, "Phone")
			if err != nil {
				t.Fatal(err)
			}
			k := newSoftKey(t)
			if tc.key != nil {
				tc.key(k)
			}
			cr := ceremony{rpID: rpID, origin: origin, challenge: challengeOf(t, opts)}
			if tc.mutate != nil {
				tc.mutate(&cr)
			}
			id, ms := steve, issued
			if tc.session != nil {
				id, ms = tc.session()
			}
			if _, err := cer.FinishRegistration(id, ms, k.create(cr)); err == nil {
				t.Fatal("accepted")
			}
			if keys.HasPending(steve.XUID) {
				t.Error("a refused registration left a pending credential")
			}
		})
	}
}

func TestRegistration_ChallengeIsSingleUseAndShortLived(t *testing.T) {
	c := &clock{time.Unix(1_790_000_000, 0)}
	cer, keys := ceremonies(t, c)
	opts, _ := cer.BeginRegistration(steve.XUID, issued, "Phone")
	k := newSoftKey(t)
	body := k.create(ceremony{rpID: rpID, origin: origin, challenge: challengeOf(t, opts)})
	if _, err := cer.FinishRegistration(steve, issued, body); err != nil {
		t.Fatal(err)
	}
	keys.Cancel(steve.XUID, issued)
	if _, err := cer.FinishRegistration(steve, issued, body); !errors.Is(err, ErrRegistrationExpired) {
		t.Errorf("a reused challenge: %v", err)
	}
	opts, _ = cer.BeginRegistration(steve.XUID, issued, "Phone")
	c.t = c.t.Add(PendingFor)
	if _, err := cer.FinishRegistration(steve, issued, newSoftKey(t).create(ceremony{rpID: rpID, origin: origin, challenge: challengeOf(t, opts)})); !errors.Is(err, ErrRegistrationExpired) {
		t.Errorf("an expired challenge: %v", err)
	}
}

func TestRegistration_RefusedWhileOneIsPending(t *testing.T) {
	c := &clock{time.Unix(1_790_000_000, 0)}
	cer, _ := ceremonies(t, c)
	opts, _ := cer.BeginRegistration(steve.XUID, issued, "Phone")
	_, _ = cer.FinishRegistration(steve, issued, newSoftKey(t).create(ceremony{rpID: rpID, origin: origin, challenge: challengeOf(t, opts)}))
	if _, err := cer.BeginRegistration(steve.XUID, issued, "Laptop"); !errors.Is(err, ErrPendingExists) {
		t.Errorf("a second registration while one is pending: %v", err)
	}
}

// loginWith runs BeginLogin and returns the challenge and its cookie.
func loginWith(t *testing.T, cer *Ceremonies) (string, *http.Cookie) {
	t.Helper()
	rec := httptest.NewRecorder()
	opts, err := cer.BeginLogin(rec)
	if err != nil {
		t.Fatal(err)
	}
	var jar *http.Cookie
	for _, ck := range rec.Result().Cookies() {
		if ck.Name == PasskeyChallengeCookie {
			jar = ck
		}
	}
	if jar == nil || !jar.HttpOnly || !jar.Secure || jar.SameSite != http.SameSiteStrictMode {
		t.Fatalf("challenge cookie = %+v", jar)
	}
	raw, _ := json.Marshal(opts)
	if bytes.Contains(raw, []byte("allowCredentials")) {
		t.Errorf("a discoverable login lists credentials: %s", raw)
	}
	return challengeOf(t, opts), jar
}

func finish(cer *Ceremonies, jar *http.Cookie, body []byte) (Passkey, error) {
	req := httptest.NewRequest("POST", "/auth/passkey/finish", bytes.NewReader(body))
	if jar != nil {
		req.AddCookie(jar)
	}
	return cer.FinishLogin(httptest.NewRecorder(), req, body)
}

func resultOf(err error) string {
	var ce *CeremonyError
	if errors.As(err, &ce) {
		return ce.Result
	}
	return "not a ceremony error: " + errorString(err)
}

func errorString(err error) string {
	if err == nil {
		return "nil"
	}
	return err.Error()
}

func TestLogin_AGoodAssertionLogsIn(t *testing.T) {
	c := &clock{time.Unix(1_790_000_000, 0)}
	cer, keys := ceremonies(t, c)
	k := newSoftKey(t)
	register(t, cer, keys, k)
	ch, jar := loginWith(t, cer)
	got, err := finish(cer, jar, k.get(ceremony{rpID: rpID, origin: origin, challenge: ch}, keys.UserHandle(steve.XUID)))
	if err != nil || got.XUID != steve.XUID || got.SignCount != k.count {
		t.Fatalf("FinishLogin = %+v, %v", got, err)
	}
}

func TestLogin_EveryRefusal(t *testing.T) {
	c := &clock{time.Unix(1_790_000_000, 0)}
	other := []byte(strings.Repeat("o", 32))
	for name, tc := range map[string]struct {
		mutate func(*ceremony)
		handle func(*Passkeys) []byte
		want   string
	}{
		"wrong type":          {mutate: func(c *ceremony) { c.typ = "webauthn.create" }, want: ResultBadAssertion},
		"wrong origin":        {mutate: func(c *ceremony) { c.origin = "https://elsewhere.example" }, want: ResultBadAssertion},
		"cross origin":        {mutate: func(c *ceremony) { c.clientData = func(m map[string]any) { m["crossOrigin"] = true } }, want: ResultBadAssertion},
		"top origin":          {mutate: func(c *ceremony) { c.clientData = func(m map[string]any) { m["topOrigin"] = origin } }, want: ResultBadAssertion},
		"wrong rp id":         {mutate: func(c *ceremony) { c.rpID = "elsewhere.example" }, want: ResultBadAssertion},
		"no user presence":    {mutate: func(c *ceremony) { c.flags = flagUV }, want: ResultBadAssertion},
		"no verification":     {mutate: func(c *ceremony) { c.flags = flagUP }, want: ResultBadAssertion},
		"wrong challenge":     {mutate: func(c *ceremony) { c.challenge = b64.EncodeToString(bytes.Repeat([]byte{9}, 32)) }, want: ResultBadAssertion},
		"no user handle":      {handle: func(*Passkeys) []byte { return nil }, want: ResultBadAssertion},
		"another's handle":    {handle: func(p *Passkeys) []byte { return p.UserHandle("2535412345678902") }, want: ResultUnknownCredential},
		"an unknown handle":   {handle: func(*Passkeys) []byte { return other }, want: ResultUnknownCredential},
	} {
		t.Run(name, func(t *testing.T) {
			cer, keys := ceremonies(t, c)
			k := newSoftKey(t)
			register(t, cer, keys, k)
			ch, jar := loginWith(t, cer)
			cr := ceremony{rpID: rpID, origin: origin, challenge: ch}
			if tc.mutate != nil {
				tc.mutate(&cr)
			}
			handle := keys.UserHandle(steve.XUID)
			if tc.handle != nil {
				handle = tc.handle(keys)
			}
			if _, err := finish(cer, jar, k.get(cr, handle)); resultOf(err) != tc.want {
				t.Errorf("result %s, want %s", resultOf(err), tc.want)
			}
		})
	}
}

func TestLogin_ChallengeCookie(t *testing.T) {
	c := &clock{time.Unix(1_790_000_000, 0)}
	cer, keys := ceremonies(t, c)
	k := newSoftKey(t)
	register(t, cer, keys, k)
	handle := keys.UserHandle(steve.XUID)

	ch, jar := loginWith(t, cer)
	if _, err := finish(cer, nil, k.get(ceremony{rpID: rpID, origin: origin, challenge: ch}, handle)); resultOf(err) != ResultExpired {
		t.Errorf("no cookie: %s", resultOf(err))
	}
	forged := *jar
	forged.Value = forged.Value[:len(forged.Value)-2] + "AA"
	if _, err := finish(cer, &forged, k.get(ceremony{rpID: rpID, origin: origin, challenge: ch}, handle)); resultOf(err) != ResultBadAssertion {
		t.Errorf("a cookie with a bad HMAC: %s", resultOf(err))
	}
	// A cookie signed under any key but the one derived with its own label.
	other, _ := NewCeremonies(origin, rpID, []byte(strings.Repeat("x", 32)), keys, c.now)
	_, foreign := loginWith(t, other)
	if _, err := finish(cer, foreign, k.get(ceremony{rpID: rpID, origin: origin, challenge: ch}, handle)); resultOf(err) != ResultBadAssertion {
		t.Errorf("a cookie from another key: %s", resultOf(err))
	}
	c.t = c.t.Add(PendingFor)
	if _, err := finish(cer, jar, k.get(ceremony{rpID: rpID, origin: origin, challenge: ch}, handle)); resultOf(err) != ResultExpired {
		t.Errorf("an expired challenge: %s", resultOf(err))
	}
	if bytes.Equal(PasskeyChallengeKey([]byte(strings.Repeat("k", 32))), []byte(strings.Repeat("k", 32))) {
		t.Error("the challenge key is the session key")
	}
}

// The same assertion sent twice at once gets exactly one session.
func TestLogin_AReplayedAssertionIsRefusedEvenAtTheSameMoment(t *testing.T) {
	c := &clock{time.Unix(1_790_000_000, 0)}
	cer, keys := ceremonies(t, c)
	k := newSoftKey(t)
	register(t, cer, keys, k)
	ch, jar := loginWith(t, cer)
	body := k.get(ceremony{rpID: rpID, origin: origin, challenge: ch}, keys.UserHandle(steve.XUID))
	var wg sync.WaitGroup
	results := make([]string, 8)
	for i := range results {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, err := finish(cer, jar, body)
			results[i] = resultOf(err)
		}()
	}
	wg.Wait()
	ok := 0
	for _, r := range results {
		switch r {
		case "not a ceremony error: nil":
			ok++
		case ResultReplayed:
		default:
			t.Errorf("unexpected result %s", r)
		}
	}
	if ok != 1 {
		t.Errorf("%d of 8 identical assertions logged in, want 1", ok)
	}
}

func TestLogin_PendingAndCounterAreJudgedAfterTheSignature(t *testing.T) {
	c := &clock{time.Unix(1_790_000_000, 0)}
	cer, keys := ceremonies(t, c)
	k := newSoftKey(t)
	opts, _ := cer.BeginRegistration(steve.XUID, issued, "Phone")
	_, _ = cer.FinishRegistration(steve, issued, k.create(ceremony{rpID: rpID, origin: origin, challenge: challengeOf(t, opts)}))
	ch, jar := loginWith(t, cer)
	if _, err := finish(cer, jar, k.get(ceremony{rpID: rpID, origin: origin, challenge: ch}, keys.UserHandle(steve.XUID))); resultOf(err) != ResultPending {
		t.Errorf("a pending credential: %s", resultOf(err))
	}

	code, _, _ := keys.IssueCode(steve.XUID)
	_, _, _ = keys.Confirm(steve.XUID, issued, k.id, code)
	ch, jar = loginWith(t, cer)
	if _, err := finish(cer, jar, k.get(ceremony{rpID: rpID, origin: origin, challenge: ch}, keys.UserHandle(steve.XUID))); err != nil {
		t.Fatal(err)
	}
	k.count -= 2 // a clone, behind the original
	ch, jar = loginWith(t, cer)
	if _, err := finish(cer, jar, k.get(ceremony{rpID: rpID, origin: origin, challenge: ch}, keys.UserHandle(steve.XUID))); resultOf(err) != ResultCounter {
		t.Errorf("a counter that went back: %s", resultOf(err))
	}
}
```

- [ ] **Step 4: Run the tests and confirm they fail**

Run: `cd minecraft/mcmap && go test -count=1 -run 'TestRegistration|TestLogin_' ./internal/auth/ </dev/null`
Expected: FAIL to compile, `undefined: NewCeremonies`.

- [ ] **Step 5: Implement**

`internal/auth/ceremony.go`:

```go
package auth

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/binary"
	"errors"
	"net/http"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/go-webauthn/webauthn/protocol"
	"github.com/go-webauthn/webauthn/protocol/webauthncose"
	"github.com/go-webauthn/webauthn/webauthn"
)

const PasskeyChallengeCookie = cookiePrefix + "mcmap_passkey"

const (
	ResultBadAssertion      = "bad_assertion"
	ResultUnknownCredential = "unknown_credential"
	ResultExpired           = "expired"
	ResultPending           = "pending"
	ResultCounter           = "counter"
	ResultReplayed          = "replayed"
)

var (
	ErrLabel               = errors.New("a label is 1 to 32 letters, digits, spaces or hyphens")
	ErrRegistrationExpired = errors.New("that registration has expired or was already used; start again")
	ErrRegistrationRefused = errors.New("that passkey was not accepted")
)

type CeremonyError struct{ Result string }

func (e *CeremonyError) Error() string { return "passkey login refused: " + e.Result }

var labelShape = regexp.MustCompile(`^[A-Za-z0-9 -]{1,32}$`)

// maxRegistrations bounds the waiting registrations. Only an admin can
// start one, so this is a backstop and not the defence.
const maxRegistrations = 64

type registration struct {
	session     webauthn.SessionData
	issuedMilli int64
	label       string
	expires     time.Time
}

// Ceremonies runs WebAuthn for the map. The library checks the signatures,
// the CBOR and most of each ceremony; the checks the spec makes stricter
// than the library's defaults (no framing, attestation "none" only, the
// counter, the user handle) are made here, so that a library upgrade
// cannot loosen them.
type Ceremonies struct {
	wa      *webauthn.WebAuthn
	rpID    string
	keys    *Passkeys
	hmacKey []byte
	now     func() time.Time

	mu   sync.Mutex
	regs map[string]registration
	used map[[sha256.Size]byte]time.Time
}

func PasskeyChallengeKey(sessionKey []byte) []byte {
	mac := hmac.New(sha256.New, sessionKey)
	mac.Write([]byte("mcmap passkey login challenge v1\x00"))
	return mac.Sum(nil)
}

func NewCeremonies(origin, rpID string, sessionKey []byte, keys *Passkeys, now func() time.Time) (*Ceremonies, error) {
	wa, err := webauthn.New(&webauthn.Config{RPID: rpID, RPDisplayName: "FWB map", RPOrigins: []string{origin}})
	if err != nil {
		return nil, err
	}
	return &Ceremonies{wa: wa, rpID: rpID, keys: keys, hmacKey: PasskeyChallengeKey(sessionKey), now: now,
		regs: map[string]registration{}, used: map[[sha256.Size]byte]time.Time{}}, nil
}

type waUser struct {
	handle []byte
	keys   []Passkey
}

func (u waUser) WebAuthnID() []byte          { return u.handle }
func (u waUser) WebAuthnName() string        { return "fwb-map" }
func (u waUser) WebAuthnDisplayName() string { return "FWB map admin" }
func (u waUser) WebAuthnCredentials() []webauthn.Credential {
	out := make([]webauthn.Credential, 0, len(u.keys))
	for _, k := range u.keys {
		out = append(out, webauthn.Credential{
			ID: k.ID, PublicKey: k.PublicKey,
			Authenticator: webauthn.Authenticator{SignCount: k.SignCount},
			Flags:         webauthn.CredentialFlags{UserPresent: true, UserVerified: true, BackupEligible: k.BackupEligible, BackupState: k.BackupState},
		})
	}
	return out
}

func (c *Ceremonies) BeginRegistration(xuid string, issuedMilli int64, label string) (*protocol.CredentialCreation, error) {
	if !labelShape.MatchString(label) || strings.TrimSpace(label) == "" {
		return nil, ErrLabel
	}
	if c.keys.HasPending(xuid) {
		return nil, ErrPendingExists
	}
	u := waUser{handle: c.keys.UserHandle(xuid), keys: c.keys.Existing(xuid)}
	var exclude []protocol.CredentialDescriptor
	for _, cred := range u.WebAuthnCredentials() {
		exclude = append(exclude, cred.Descriptor())
	}
	creation, session, err := c.wa.BeginRegistration(u,
		webauthn.WithAuthenticatorSelection(protocol.AuthenticatorSelection{
			ResidentKey: protocol.ResidentKeyRequirementRequired, RequireResidentKey: protocol.ResidentKeyRequired(),
			UserVerification: protocol.VerificationRequired,
		}),
		webauthn.WithConveyancePreference(protocol.PreferNoAttestation),
		webauthn.WithExclusions(exclude),
		webauthn.WithCredentialParameters([]protocol.CredentialParameter{
			{Type: protocol.PublicKeyCredentialType, Algorithm: webauthncose.AlgES256},
			{Type: protocol.PublicKeyCredentialType, Algorithm: webauthncose.AlgRS256},
		}),
	)
	if err != nil {
		return nil, err
	}
	// The library's expiry reads the wall clock; the map's own, below, is
	// the one that counts, and the tests' clock drives it.
	session.Expires = time.Time{}
	c.mu.Lock()
	defer c.mu.Unlock()
	now := c.now()
	for x, r := range c.regs {
		if !now.Before(r.expires) {
			delete(c.regs, x)
		}
	}
	if len(c.regs) >= maxRegistrations {
		return nil, ErrRegistrationRefused
	}
	c.regs[xuid] = registration{session: *session, issuedMilli: issuedMilli, label: label, expires: now.Add(PendingFor)}
	return creation, nil
}

func (c *Ceremonies) FinishRegistration(id Identity, issuedMilli int64, body []byte) (Passkey, error) {
	c.mu.Lock()
	reg, ok := c.regs[id.XUID]
	if ok {
		delete(c.regs, id.XUID)
	}
	c.mu.Unlock()
	if !ok || reg.issuedMilli != issuedMilli || !c.now().Before(reg.expires) {
		return Passkey{}, ErrRegistrationExpired
	}
	parsed, err := protocol.ParseCredentialCreationResponseBytes(body)
	if err != nil {
		return Passkey{}, ErrRegistrationRefused
	}
	cd := parsed.Response.CollectedClientData
	if cd.Type != protocol.CreateCeremony || cd.CrossOrigin || cd.TopOrigin != "" ||
		parsed.Response.AttestationObject.Format != "none" || len(parsed.RawID) > 1023 {
		return Passkey{}, ErrRegistrationRefused
	}
	u := waUser{handle: c.keys.UserHandle(id.XUID), keys: c.keys.Existing(id.XUID)}
	cred, err := c.wa.CreateCredential(u, reg.session, parsed)
	if err != nil {
		return Passkey{}, ErrRegistrationRefused
	}
	k := Passkey{
		ID: cred.ID, PublicKey: cred.PublicKey, UserHandle: u.handle, SignCount: cred.Authenticator.SignCount,
		BackupEligible: cred.Flags.BackupEligible, BackupState: cred.Flags.BackupState,
		XUID: id.XUID, Gamertag: id.Gamertag, Label: reg.label,
	}
	if err := c.keys.StartPending(k, issuedMilli); err != nil {
		return Passkey{}, err
	}
	return k, nil
}

func (c *Ceremonies) BeginLogin(w http.ResponseWriter) (*protocol.CredentialAssertion, error) {
	assertion, session, err := c.wa.BeginDiscoverableLogin(webauthn.WithUserVerification(protocol.VerificationRequired))
	if err != nil {
		return nil, err
	}
	challenge, err := b64Decode(session.Challenge)
	if err != nil {
		return nil, err
	}
	expires := c.now().Add(PendingFor)
	http.SetCookie(w, &http.Cookie{
		Name: PasskeyChallengeCookie, Value: c.seal(challenge, expires), Path: "/",
		MaxAge: int(PendingFor.Seconds()), HttpOnly: true, Secure: true, SameSite: http.SameSiteStrictMode,
	})
	return assertion, nil
}

// seal is the challenge, its expiry and an HMAC over both: everything the
// map needs to judge the answer, kept by the browser, so that a flood of
// requests for challenges costs the map nothing to hold.
func (c *Ceremonies) seal(challenge []byte, expires time.Time) string {
	payload := binary.BigEndian.AppendUint64(append([]byte{}, challenge...), uint64(expires.Unix()))
	mac := hmac.New(sha256.New, c.hmacKey)
	mac.Write(payload)
	return encoding.EncodeToString(mac.Sum(payload))
}

func (c *Ceremonies) unseal(value string) (challenge []byte, result string) {
	raw, err := encoding.DecodeString(value)
	if err != nil || len(raw) < 8+sha256.Size+16 {
		return nil, ResultBadAssertion
	}
	payload, sum := raw[:len(raw)-sha256.Size], raw[len(raw)-sha256.Size:]
	mac := hmac.New(sha256.New, c.hmacKey)
	mac.Write(payload)
	if !hmac.Equal(sum, mac.Sum(nil)) {
		return nil, ResultBadAssertion
	}
	challenge, at := payload[:len(payload)-8], binary.BigEndian.Uint64(payload[len(payload)-8:])
	if !c.now().Before(time.Unix(int64(at), 0)) {
		return nil, ResultExpired
	}
	return challenge, ""
}

func refuse(result string) error { return &CeremonyError{Result: result} }

var errUnknownCredential = errors.New("unknown credential")

func (c *Ceremonies) FinishLogin(w http.ResponseWriter, r *http.Request, body []byte) (Passkey, error) {
	jar, err := r.Cookie(PasskeyChallengeCookie)
	if err != nil {
		return Passkey{}, refuse(ResultExpired)
	}
	http.SetCookie(w, &http.Cookie{Name: PasskeyChallengeCookie, Path: "/", MaxAge: -1, HttpOnly: true, Secure: true, SameSite: http.SameSiteStrictMode})
	challenge, result := c.unseal(jar.Value)
	if result != "" {
		return Passkey{}, refuse(result)
	}
	parsed, err := protocol.ParseCredentialRequestResponseBytes(body)
	if err != nil {
		return Passkey{}, refuse(ResultBadAssertion)
	}
	cd := parsed.Response.CollectedClientData
	if cd.Type != protocol.AssertCeremony || cd.CrossOrigin || cd.TopOrigin != "" || len(parsed.Response.UserHandle) == 0 {
		return Passkey{}, refuse(ResultBadAssertion)
	}
	var found Passkey
	var pending bool
	handler := func(rawID, userHandle []byte) (webauthn.User, error) {
		_, k, p, ok := c.keys.Lookup(userHandle, rawID)
		if !ok {
			return nil, errUnknownCredential
		}
		found, pending = k, p
		return waUser{handle: userHandle, keys: []Passkey{k}}, nil
	}
	session := webauthn.SessionData{Challenge: encoding.EncodeToString(challenge), RelyingPartyID: c.rpID, UserVerification: protocol.VerificationRequired}
	_, _, err = c.wa.ValidatePasskeyLogin(handler, session, parsed)
	switch {
	case errors.Is(err, errUnknownCredential):
		return Passkey{}, refuse(ResultUnknownCredential)
	case err != nil:
		return Passkey{}, refuse(ResultBadAssertion)
	}
	// From here the signature is good, and a refusal returns the passkey
	// it was for, so that the caller can say whose it was. The challenge is spent in one step,
	// so two copies of one assertion cannot both pass.
	spent := sha256.Sum256(challenge)
	c.mu.Lock()
	now := c.now()
	for h, until := range c.used {
		if !now.Before(until) {
			delete(c.used, h)
		}
	}
	_, replayed := c.used[spent]
	if !replayed {
		c.used[spent] = now.Add(PendingFor)
	}
	c.mu.Unlock()
	if replayed {
		return found, refuse(ResultReplayed)
	}
	if pending {
		return found, refuse(ResultPending)
	}
	// The count is read from what the authenticator signed and not from
	// the library's credential: on a count that went back the library
	// keeps the old one and only raises CloneWarning, and the map refuses.
	flags := parsed.Response.AuthenticatorData.Flags
	k, err := c.keys.Use(found.ID, parsed.Response.AuthenticatorData.Counter, flags.HasBackupEligible(), flags.HasBackupState())
	if err != nil {
		return found, refuse(ResultCounter)
	}
	return k, nil
}

func b64Decode(s string) ([]byte, error) { return encoding.DecodeString(strings.TrimRight(s, "=")) }
```

At v0.18.2 the library does not fail a login whose counter went back: `Authenticator.UpdateCounter` keeps the stored count and sets `CloneWarning`. That is why the map reads the count from `parsed.Response.AuthenticatorData.Counter` and judges it itself in `Passkeys.Use`.

- [ ] **Step 6: Run the tests and confirm they pass**

Run: `cd minecraft/mcmap && go test -count=1 -race ./internal/auth/ </dev/null`
Expected: PASS. If a "wrong origin" or "top origin" case passes through the library, keep the map's own check above the library call, which is where it is, and confirm the case is caught there.

- [ ] **Step 7: Commit**

```bash
gofmt -l minecraft/mcmap/internal/auth && go vet ./minecraft/mcmap/internal/auth/
/usr/bin/git add go.mod go.sum minecraft/mcmap/internal/auth/ceremony.go minecraft/mcmap/internal/auth/ceremony_test.go minecraft/mcmap/internal/auth/softkey_test.go
/usr/bin/git commit -m "feat(mcmap): verify passkey registration and login, stricter than the library's defaults" -m "Pins github.com/go-webauthn/webauthn at v0.18.2.

Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>
Assisted-by: Claude Code:claude-opus-5-5"
```

### Task 6: The admin gate, `/api/me` and `/api/config`

**Files:**
- Create: `minecraft/mcmap/internal/server/admin.go`, `minecraft/mcmap/internal/server/admin_metrics.go`
- Modify: `minecraft/mcmap/internal/server/server.go` (fields; the `gate` constants; `route`), `minecraft/mcmap/internal/server/auth.go` (`handleConfig`, `handleMe`)
- Test: `minecraft/mcmap/internal/server/admin_test.go` (create), `minecraft/mcmap/internal/server/service_test.go` (modify the listener rule and `withService`)

**Interfaces:**
- Consumes: `operators.Source` (Task 2), `auth.SessionFromContext` (Task 3), `auth.Passkeys` and `auth.Ceremonies` (Tasks 4 and 5).
- Produces:
  ```go
  // in package server
  type OperatorList interface {
      IsOperator(ctx context.Context, xuid string) (bool, operators.Source)
      Source() operators.Source
  }
  // Server fields:
  Admin      bool
  Operators  OperatorList
  Passkeys   *auth.Passkeys
  Ceremonies *auth.Ceremonies
  Quiet      schedule.Quiet
  // gate:
  gateAdmin
  func (s *Server) adminOn() bool
  func (s *Server) passkeysOn() bool
  func (s *Server) isAdmin(ctx context.Context, xuid string) bool
  func CountPasskeys(pending, active int)   // Passkeys.Counted
  func CountPasskeyExpired()                // Passkeys.Expired
  func declareAdminMetrics()
  ```
  `/api/me` gains `"admin": true` for an operator, and `/api/config` gains `"passkeys": true|false`.

- [ ] **Step 1: Write the failing tests**

`internal/server/admin_test.go`:

```go
package server

import (
	"context"
	"net/http"
	"strings"
	"testing"

	"github.com/prometheus/client_golang/prometheus/testutil"

	"github.com/jdwillmsen/gameops/minecraft/mcmap/internal/auth"
	"github.com/jdwillmsen/gameops/minecraft/mcmap/internal/operators"
)

var alex = auth.Identity{XUID: "2535412345678902", Gamertag: "Alex Mines"}

// fakeOperators is the operator list as a set, always read live.
type fakeOperators map[string]bool

func (f fakeOperators) IsOperator(_ context.Context, xuid string) (bool, operators.Source) {
	return f[xuid], operators.Live
}
func (f fakeOperators) Source() operators.Source { return operators.Live }

func withAdmin(t *testing.T, ops fakeOperators) *Server {
	t.Helper()
	s := withLogin(t)
	s.Admin, s.Operators = true, ops
	return s
}

func TestAdmin_MeSaysSoForAnOperatorOnly(t *testing.T) {
	s := withAdmin(t, fakeOperators{steve.XUID: true})
	h := s.Handler()
	if rec := do(h, "GET", "/api/me", "", []*http.Cookie{session(s, steve)}); !strings.Contains(rec.Body.String(), `"admin":true`) {
		t.Errorf("an operator's /api/me = %s", rec.Body)
	}
	if rec := do(h, "GET", "/api/me", "", []*http.Cookie{session(s, alex)}); strings.Contains(rec.Body.String(), `admin`) {
		t.Errorf("a member's /api/me = %s", rec.Body)
	}
	s.Admin = false
	if rec := do(s.Handler(), "GET", "/api/me", "", []*http.Cookie{session(s, steve)}); strings.Contains(rec.Body.String(), `admin`) {
		t.Errorf("with admin off, /api/me = %s", rec.Body)
	}
}

func TestAdmin_TheGate(t *testing.T) {
	s := withAdmin(t, fakeOperators{steve.XUID: true})
	reached := 0
	gate := s.Sessions.Require(s.requireAdmin("GET /test", func(http.ResponseWriter, *http.Request) { reached++ }))
	if rec := do(gate, "GET", "/test", "", nil); rec.Code != http.StatusUnauthorized {
		t.Errorf("no session = %d", rec.Code)
	}
	if rec := do(gate, "GET", "/test", "", []*http.Cookie{session(s, alex)}); rec.Code != http.StatusForbidden {
		t.Errorf("a member = %d", rec.Code)
	}
	before := testutil.ToFloat64(metricAdminRequests.WithLabelValues("GET /test", "true"))
	if rec := do(gate, "GET", "/test", "", []*http.Cookie{session(s, steve)}); rec.Code != http.StatusOK || reached != 1 {
		t.Errorf("an operator = %d, reached %d", rec.Code, reached)
	}
	if testutil.ToFloat64(metricAdminRequests.WithLabelValues("GET /test", "true")) != before+1 {
		t.Error("an admin request was not counted")
	}
	// Demoted: the same cookie, the next lookup.
	s.Operators = fakeOperators{}
	if rec := do(gate, "GET", "/test", "", []*http.Cookie{session(s, steve)}); rec.Code != http.StatusForbidden {
		t.Errorf("a demoted operator's cookie = %d", rec.Code)
	}
}

// With no operator list to read, nobody is an admin and nothing errors.
func TestAdmin_NoListMeansNobodyAndNoError(t *testing.T) {
	s := withAdmin(t, nil)
	s.Operators = nil
	if rec := do(s.Handler(), "GET", "/api/me", "", []*http.Cookie{session(s, steve)}); rec.Code != http.StatusOK || strings.Contains(rec.Body.String(), "admin") {
		t.Errorf("/api/me with no list = %d %s", rec.Code, rec.Body)
	}
}

func TestConfig_SaysWhetherThereIsAPasskeyLogin(t *testing.T) {
	s := withAdmin(t, fakeOperators{})
	if rec := do(s.Handler(), "GET", "/api/config", "", nil); !strings.Contains(rec.Body.String(), `"passkeys":false`) {
		t.Errorf("/api/config without passkeys = %s", rec.Body)
	}
	withPasskeys(t, s)
	if rec := do(s.Handler(), "GET", "/api/config", "", nil); !strings.Contains(rec.Body.String(), `"passkeys":true`) {
		t.Errorf("/api/config with passkeys = %s", rec.Body)
	}
}

func TestAdminMetrics_EveryLabelIsDeclaredAtZero(t *testing.T) {
	declareAdminMetrics()
	for _, r := range registrationResults {
		if _, err := metricPasskeysRegistered.GetMetricWithLabelValues(r); err != nil {
			t.Errorf("registered %s: %v", r, err)
		}
	}
	for _, r := range loginResults {
		if _, err := metricAdminLogins.GetMetricWithLabelValues(r); err != nil {
			t.Errorf("logins %s: %v", r, err)
		}
	}
	if n := testutil.CollectAndCount(metricPasskeysRegistered); n < len(registrationResults) {
		t.Errorf("only %d registration series exist after declaring", n)
	}
	if n := testutil.CollectAndCount(metricAdminLogins); n < len(loginResults) {
		t.Errorf("only %d login series exist after declaring", n)
	}
	if n := testutil.CollectAndCount(metricPasskeys); n != 2 {
		t.Errorf("%d passkeys gauge series, want pending and active", n)
	}
}
```

Add this helper to `admin_test.go`. Task 7 uses it too:

```go
// withPasskeys switches passkeys on for s, against the test origin.
func withPasskeys(t *testing.T, s *Server) (*auth.Passkeys, *auth.Ceremonies) {
	t.Helper()
	keys, err := auth.LoadPasskeys(t.TempDir()+"/passkeys.json", time.Now)
	if err != nil {
		t.Fatal(err)
	}
	cer, err := auth.NewCeremonies("https://map.example", "map.example", s.Sessions.Key, keys, time.Now)
	if err != nil {
		t.Fatal(err)
	}
	s.Passkeys, s.Ceremonies = keys, cer
	s.Sessions.Passkeys, s.Sessions.PasskeyTTL = keys, 12*time.Hour
	return keys, cer
}
```

Add `"time"` to the imports.

In `service_test.go`, change the listener rule in `TestService_EveryRouteHasAGateThatWasChosenForIt` to:

```go
		if (gate == "gateViewer" || gate == "gatePlayer" || gate == "gateAdmin") != (listener == "public") && gate != "gatePublic" {
```

In `withService`, after `s.Service = …`, switch admin and passkeys on with an operator list that does not hold Steve, so that every admin route can be asked as a player and as a service session:

```go
	s.Admin, s.Operators = true, fakeOperators{}
	withPasskeys(t, s)
```

In `TestService_ASessionReachesWhatIsShownAndIsForbiddenTheRest`, make the generic check skip `gateAdmin`, and add the case:

```go
		if route.gate != "gateAdmin" && (asPlayer.Code == http.StatusForbidden || asPlayer.Code == http.StatusUnauthorized) {
			t.Errorf("%s as a player = %d", pattern, asPlayer.Code)
		}
		switch route.gate {
		case "gateAdmin":
			// Steve is no operator here: both are turned away, and the
			// service session is turned away before the list is read.
			wantForbidden++
			if asService.Code != http.StatusForbidden || asPlayer.Code != http.StatusForbidden {
				t.Errorf("%s: service %d, member %d; want 403 for both", pattern, asService.Code, asPlayer.Code)
			}
```

Keep the existing `gatePlayer`, `gateViewer` and `default` cases after it.

- [ ] **Step 2: Run the tests and confirm they fail**

Run: `cd minecraft/mcmap && go test -count=1 -run 'TestAdmin|TestConfig_SaysWhether|TestAdminMetrics' ./internal/server/ </dev/null`
Expected: FAIL to compile, `s.Admin undefined`.

- [ ] **Step 3: Implement**

In `server.go`, add to `Server` after `Service`:

```go
	// Admin is whether the game's operators are the map's admins, and
	// Operators the list that says who they are. Either off, there are none.
	Admin     bool
	Operators OperatorList
	// Passkeys and Ceremonies are the passkey login. Nil, there is none.
	Passkeys   *auth.Passkeys
	Ceremonies *auth.Ceremonies
	// Quiet is the map's quiet windows, for the status to say whether it is
	// in one.
	Quiet schedule.Quiet
```

Add `"github.com/jdwillmsen/gameops/minecraft/mcmap/internal/schedule"` to the imports. Add to the `gate` constants, after `gateExchange`:

```go
	// gateAdmin is a player's session whose XUID the game's operator list
	// holds, read when the request arrives. A service session has no XUID
	// and is refused before the list is read.
	gateAdmin
```

In `route`, add the case:

```go
	case gateAdmin:
		if s.Sessions != nil {
			handler = s.Sessions.Require(s.requireAdmin(pattern, h))
		}
```

`internal/server/admin.go`:

```go
package server

import (
	"context"
	"net/http"
	"strconv"

	"github.com/jdwillmsen/gameops/minecraft/mcmap/internal/auth"
	"github.com/jdwillmsen/gameops/minecraft/mcmap/internal/operators"
)

type OperatorList interface {
	IsOperator(ctx context.Context, xuid string) (bool, operators.Source)
	Source() operators.Source
}

func (s *Server) adminOn() bool { return s.Admin && s.Sessions != nil && s.Operators != nil }

func (s *Server) passkeysOn() bool { return s.adminOn() && s.Passkeys != nil && s.Ceremonies != nil }

func (s *Server) isAdmin(ctx context.Context, xuid string) bool {
	if !s.adminOn() {
		return false
	}
	ok, _ := s.Operators.IsOperator(ctx, xuid)
	return ok
}

// requireAdmin is gateAdmin past the session check. The answer is the
// operator list's at the moment of the request, so a demotion closes every
// admin route to cookies issued before it.
func (s *Server) requireAdmin(pattern string, next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id, ok := auth.FromContext(r.Context())
		allowed := ok && s.isAdmin(r.Context(), id.XUID)
		metricAdminRequests.WithLabelValues(pattern, strconv.FormatBool(allowed)).Inc()
		s.log().Info("map admin request", "xuid", id.XUID, "route", pattern, "allowed", allowed)
		if !allowed {
			w.Header().Set("Cache-Control", "no-store")
			http.Error(w, "not open to this player", http.StatusForbidden)
			return
		}
		next(w, r)
	}
}
```

`internal/server/admin_metrics.go`:

```go
package server

import (
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
)

var registrationResults = []string{"pending", "refused", "code_issued", "code_wrong", "abandoned", "cancelled", "expired", "confirmed"}

// The results before the signature verifies are anyone's to make; those
// after it mean a real passkey was turned away, which is what pages.
var loginResults = []string{"ok", "bad_assertion", "unknown_credential", "expired", "disabled", "pending", "not_operator", "counter", "replayed"}

var (
	metricPasskeysRegistered = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "mcmap_admin_passkeys_registered_total",
		Help: "Steps of adding a passkey, by result: pending, refused, code_issued, code_wrong, abandoned, cancelled, expired, confirmed.",
	}, []string{"result"})
	metricPasskeysRemoved = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "mcmap_admin_passkeys_removed_total",
		Help: "Passkeys removed, by why: player (from the page) or revoked (!map logout or Log out everywhere).",
	}, []string{"why"})
	metricAdminLogins = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "mcmap_admin_logins_total",
		Help: "Passkey logins, by result. bad_assertion, unknown_credential, expired and disabled come before the signature is verified; pending, not_operator, counter and replayed after.",
	}, []string{"result"})
	metricAdminRequests = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "mcmap_admin_requests_total",
		Help: "Requests to admin routes, by route pattern and whether the player was an admin.",
	}, []string{"route", "allowed"})
	metricPasskeys = promauto.NewGaugeVec(prometheus.GaugeOpts{
		Name: "mcmap_admin_passkeys",
		Help: "Passkeys held, by state: pending or active. Read back from the volume at start, so a restart does not move it.",
	}, []string{"state"})
)

// declareAdminMetrics puts every series an alert reads at zero before its
// first event, so that the first confirmation on a new pod is an increase.
func declareAdminMetrics() {
	for _, r := range registrationResults {
		metricPasskeysRegistered.WithLabelValues(r)
	}
	for _, r := range loginResults {
		metricAdminLogins.WithLabelValues(r)
	}
	for _, why := range []string{"player", "revoked"} {
		metricPasskeysRemoved.WithLabelValues(why)
	}
	metricPasskeys.WithLabelValues("pending")
	metricPasskeys.WithLabelValues("active")
}

func CountPasskeys(pending, active int) {
	metricPasskeys.WithLabelValues("pending").Set(float64(pending))
	metricPasskeys.WithLabelValues("active").Set(float64(active))
}

func CountPasskeyExpired() { metricPasskeysRegistered.WithLabelValues("expired").Inc() }
```

In `auth.go`, replace `handleConfig` and `handleMe`:

```go
func (s *Server) handleConfig(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{"login": s.Sessions != nil, "passkeys": s.passkeysOn()})
}

func (s *Server) handleMe(w http.ResponseWriter, r *http.Request) {
	viewer, _ := auth.ViewerFromContext(r.Context())
	out := map[string]any{"gamertag": viewer.Name}
	if viewer.Service {
		out["service"] = true
	}
	if id, ok := auth.FromContext(r.Context()); ok && s.isAdmin(r.Context(), id.XUID) {
		out["admin"] = true
	}
	writeJSON(w, http.StatusOK, out)
}
```

- [ ] **Step 4: Run the package's tests and confirm they pass**

Run: `cd minecraft/mcmap && go test -count=1 -race ./internal/server/ </dev/null`
Expected: PASS. `TestService_EveryRouteHasAGateThatWasChosenForIt` still passes, because no `gateAdmin` route exists yet.

- [ ] **Step 5: Commit**

```bash
gofmt -l minecraft/mcmap/internal/server && go vet ./minecraft/mcmap/internal/server/
/usr/bin/git add minecraft/mcmap/internal/server/admin.go minecraft/mcmap/internal/server/admin_metrics.go minecraft/mcmap/internal/server/admin_test.go minecraft/mcmap/internal/server/server.go minecraft/mcmap/internal/server/auth.go minecraft/mcmap/internal/server/service_test.go
/usr/bin/git commit -m "feat(mcmap): add the admin gate, read from the operator list on every request" -m "Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>
Assisted-by: Claude Code:claude-opus-5-5"
```

### Task 7: Passkey registration and login routes

**Files:**
- Create: `minecraft/mcmap/internal/server/passkeys.go`
- Modify: `minecraft/mcmap/internal/server/server.go` (`Handler`)
- Test: `minecraft/mcmap/internal/server/passkeys_test.go` (create), `minecraft/mcmap/internal/server/service_test.go` (the `routes` table)

**Interfaces:**
- Consumes: Tasks 3 to 6.
- Produces routes on the public listener:
  - `POST /auth/passkey/options` (gatePublic) answers with `protocol.CredentialAssertion` JSON and sets the challenge cookie.
  - `POST /auth/passkey/finish` (gatePublic): the body is the credential JSON. It answers `200 {"state":"ok","gamertag":…}` with the session cookie, or `401 {"error":"<result>"}`.
  - `POST /auth/passkey/register/options` (gateAdmin): the body is `{"label":…}`. It answers `200 <CredentialCreation>`, `400 {"error":"label"}`, `409 {"error":"pending"}` or `503 {"error":"busy"}`.
  - `POST /auth/passkey/register/finish` (gateAdmin): the body is the credential JSON. It answers `200 {"fingerprint":"XXXX-XXXX","expiresIn":120}`, `409 {"error":"pending"|"too_many"}`, `410 {"error":"expired"}` or `400 {"error":"refused"}`.
  - `POST /auth/passkey/register/confirm` (gateAdmin): the body is `{"credentialId":"<base64url>","code":"…"}`. It answers `204`, `400 {"error":"wrong_code","triesLeft":n}`, `410 {"error":"abandoned"}` or `404 {"error":"nothing_pending"}`.
  - `POST /auth/passkey/register/cancel` (gateAdmin) answers `204`.
  - On the internal listener, `POST /internal/v1/passkey-codes` (**gateAgent**), which Group 2 consumes exactly. The request is `{"xuid":"<digits>"}`. It answers `200 {"code":"ABC234","expiresIn":120,"fingerprint":"7F3A-91C2"}`, `404` when nothing is pending, `400` for a malformed request, or `401` without the token.

- [ ] **Step 1: Write the failing tests**

`internal/server/passkeys_test.go`. The software authenticator lives in package `auth`'s tests, so this file drives the routes with a small one of its own, built on the same library:

```go
package server

import (
	"bytes"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/go-webauthn/webauthn/protocol/webauthncbor"
	"github.com/prometheus/client_golang/prometheus/testutil"

	"github.com/jdwillmsen/gameops/minecraft/mcmap/internal/auth"
)

var b64 = base64.RawURLEncoding

type device struct {
	t     *testing.T
	priv  *ecdsa.PrivateKey
	id    []byte
	count uint32
}

func newDevice(t *testing.T) *device {
	priv, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	id := make([]byte, 32)
	_, _ = rand.Read(id)
	return &device{t: t, priv: priv, id: id}
}

func clientData(typ, challenge string) []byte {
	raw, _ := json.Marshal(map[string]any{"type": typ, "challenge": challenge, "origin": "https://map.example", "crossOrigin": false})
	return raw
}

func (d *device) create(challenge string) string {
	x, y := d.priv.PublicKey.X.FillBytes(make([]byte, 32)), d.priv.PublicKey.Y.FillBytes(make([]byte, 32))
	cose, _ := webauthncbor.Marshal(map[int]any{1: 2, 3: -7, -1: 1, -2: x, -3: y})
	h := sha256.Sum256([]byte("map.example"))
	ad := append(append(h[:], 0x45), 0, 0, 0, 0)
	ad = append(ad, make([]byte, 16)...)
	ad = binary.BigEndian.AppendUint16(ad, uint16(len(d.id)))
	ad = append(append(ad, d.id...), cose...)
	att, _ := webauthncbor.Marshal(map[string]any{"fmt": "none", "attStmt": map[string]any{}, "authData": ad})
	body, _ := json.Marshal(map[string]any{"id": b64.EncodeToString(d.id), "rawId": b64.EncodeToString(d.id), "type": "public-key",
		"response": map[string]any{"clientDataJSON": b64.EncodeToString(clientData("webauthn.create", challenge)), "attestationObject": b64.EncodeToString(att)}})
	return string(body)
}

func (d *device) get(challenge string, handle []byte) string {
	d.count++
	h := sha256.Sum256([]byte("map.example"))
	ad := binary.BigEndian.AppendUint32(append(h[:], 0x05), d.count)
	cdj := clientData("webauthn.get", challenge)
	ch := sha256.Sum256(cdj)
	digest := sha256.Sum256(append(append([]byte{}, ad...), ch[:]...))
	sig, _ := ecdsa.SignASN1(rand.Reader, d.priv, digest[:])
	body, _ := json.Marshal(map[string]any{"id": b64.EncodeToString(d.id), "rawId": b64.EncodeToString(d.id), "type": "public-key",
		"response": map[string]any{"clientDataJSON": b64.EncodeToString(cdj), "authenticatorData": b64.EncodeToString(ad), "signature": b64.EncodeToString(sig), "userHandle": b64.EncodeToString(handle)}})
	return string(body)
}

func challengeIn(t *testing.T, body []byte) string {
	t.Helper()
	var o struct {
		PublicKey struct {
			Challenge string `json:"challenge"`
		} `json:"publicKey"`
	}
	if err := json.Unmarshal(body, &o); err != nil || o.PublicKey.Challenge == "" {
		t.Fatalf("no challenge in %s", body)
	}
	return o.PublicKey.Challenge
}

// addPasskey runs the whole of D3's adding, the agent's part included, as
// the operator whose game-code session is given.
func addPasskey(t *testing.T, s *Server, as *http.Cookie, d *device) {
	t.Helper()
	h, internal := s.Handler(), s.InternalHandler()
	opts := do(h, "POST", "/auth/passkey/register/options", `{"label":"Phone"}`, []*http.Cookie{as})
	if opts.Code != http.StatusOK {
		t.Fatalf("register options = %d %s", opts.Code, opts.Body)
	}
	fin := do(h, "POST", "/auth/passkey/register/finish", d.create(challengeIn(t, opts.Body.Bytes())), []*http.Cookie{as})
	if fin.Code != http.StatusOK || !strings.Contains(fin.Body.String(), auth.Fingerprint(d.id)) {
		t.Fatalf("register finish = %d %s", fin.Code, fin.Body)
	}
	code := do(internal, "POST", "/internal/v1/passkey-codes", `{"xuid":"`+steve.XUID+`"}`, nil, "Authorization", "Bearer "+internalToken)
	var got struct{ Code, Fingerprint string }
	_ = json.Unmarshal(code.Body.Bytes(), &got)
	if code.Code != http.StatusOK || got.Fingerprint != auth.Fingerprint(d.id) {
		t.Fatalf("passkey code = %d %s", code.Code, code.Body)
	}
	confirm := do(h, "POST", "/auth/passkey/register/confirm", `{"credentialId":"`+b64.EncodeToString(d.id)+`","code":"`+got.Code+`"}`, []*http.Cookie{as})
	if confirm.Code != http.StatusNoContent {
		t.Fatalf("confirm = %d %s", confirm.Code, confirm.Body)
	}
}

func passkeyLogin(t *testing.T, s *Server, d *device) *httptest.ResponseRecorder {
	t.Helper()
	h := s.Handler()
	opts := do(h, "POST", "/auth/passkey/options", "", nil)
	jar := cookie(opts, auth.PasskeyChallengeCookie)
	return do(h, "POST", "/auth/passkey/finish", d.get(challengeIn(t, opts.Body.Bytes()), s.Passkeys.UserHandle(steve.XUID)), []*http.Cookie{jar})
}

func TestPasskey_AddThenLogInWithoutTheGame(t *testing.T) {
	s := withAdmin(t, fakeOperators{steve.XUID: true})
	withPasskeys(t, s)
	var log bytes.Buffer
	s.Log = slog.New(slog.NewJSONHandler(&log, nil))
	declareAdminMetrics()
	confirmed := testutil.ToFloat64(metricPasskeysRegistered.WithLabelValues("confirmed"))

	d := newDevice(t)
	addPasskey(t, s, session(s, steve), d)
	if testutil.ToFloat64(metricPasskeysRegistered.WithLabelValues("confirmed")) != confirmed+1 {
		t.Error("a confirmation was not counted")
	}
	rec := passkeyLogin(t, s, d)
	sess := cookie(rec, "__Host-mcmap_session")
	if rec.Code != http.StatusOK || sess == nil || sess.MaxAge != int((12*time.Hour).Seconds()) {
		t.Fatalf("passkey login = %d %s, cookie %+v", rec.Code, rec.Body, sess)
	}
	if me := do(s.Handler(), "GET", "/api/me", "", []*http.Cookie{sess}); !strings.Contains(me.Body.String(), `"admin":true`) {
		t.Errorf("/api/me after a passkey login = %s", me.Body)
	}
	for _, never := range []string{steve.Gamertag, "Phone", b64.EncodeToString(d.id)} {
		if strings.Contains(log.String(), never) {
			t.Errorf("the log holds %q:\n%s", never, log.String())
		}
	}
	for _, line := range []string{"map passkey pending", "map passkey code issued", "map passkey registered", `"method":"passkey"`} {
		if !strings.Contains(log.String(), line) {
			t.Errorf("the log lacks %s", line)
		}
	}
}

func TestPasskey_ANonOperatorWithAPasskeyIsRefusedAfterTheSignature(t *testing.T) {
	ops := fakeOperators{steve.XUID: true}
	s := withAdmin(t, ops)
	withPasskeys(t, s)
	d := newDevice(t)
	addPasskey(t, s, session(s, steve), d)
	delete(ops, steve.XUID)
	declareAdminMetrics()
	before := testutil.ToFloat64(metricAdminLogins.WithLabelValues("not_operator"))
	if rec := passkeyLogin(t, s, d); rec.Code != http.StatusUnauthorized || cookie(rec, "__Host-mcmap_session") != nil {
		t.Errorf("a demoted holder's login = %d", rec.Code)
	}
	if testutil.ToFloat64(metricAdminLogins.WithLabelValues("not_operator")) != before+1 {
		t.Error("not counted as not_operator")
	}
}

func TestPasskey_RegistrationStatuses(t *testing.T) {
	s := withAdmin(t, fakeOperators{steve.XUID: true})
	withPasskeys(t, s)
	as := session(s, steve)
	h := s.Handler()
	if rec := do(h, "POST", "/auth/passkey/register/options", `{"label":"Café"}`, []*http.Cookie{as}); rec.Code != http.StatusBadRequest {
		t.Errorf("a bad label = %d", rec.Code)
	}
	opts := do(h, "POST", "/auth/passkey/register/options", `{"label":"Phone"}`, []*http.Cookie{as})
	d := newDevice(t)
	_ = do(h, "POST", "/auth/passkey/register/finish", d.create(challengeIn(t, opts.Body.Bytes())), []*http.Cookie{as})
	if rec := do(h, "POST", "/auth/passkey/register/options", `{"label":"Laptop"}`, []*http.Cookie{as}); rec.Code != http.StatusConflict {
		t.Errorf("a second while one is pending = %d", rec.Code)
	}
	for i, want := range []int{http.StatusBadRequest, http.StatusBadRequest, http.StatusGone} {
		rec := do(h, "POST", "/auth/passkey/register/confirm", `{"credentialId":"`+b64.EncodeToString(d.id)+`","code":"ZZZZZZ"}`, []*http.Cookie{as})
		if rec.Code != want {
			t.Errorf("wrong code %d = %d %s", i+1, rec.Code, rec.Body)
		}
	}
	if rec := do(h, "POST", "/auth/passkey/register/confirm", `{"credentialId":"`+b64.EncodeToString(d.id)+`","code":"ABC234"}`, []*http.Cookie{as}); rec.Code != http.StatusNotFound {
		t.Errorf("after abandonment = %d", rec.Code)
	}
	if rec := do(h, "POST", "/auth/passkey/register/cancel", "", []*http.Cookie{as}); rec.Code != http.StatusNoContent {
		t.Errorf("cancel = %d", rec.Code)
	}
}

// Review focus: garbage on the public finish route is a counted
// bad_assertion, read no further than the cap.
func TestPasskeyLogin_GarbageIsABadAssertionNotAFault(t *testing.T) {
	s := withAdmin(t, fakeOperators{steve.XUID: true})
	withPasskeys(t, s)
	h := s.Handler()
	declareAdminMetrics()
	before := testutil.ToFloat64(metricAdminLogins.WithLabelValues("bad_assertion"))
	for name, body := range map[string]string{
		"not json":   "{{{",
		"bad base64": `{"id":"!!","rawId":"!!","type":"public-key","response":{}}`,
		"a megabyte": `{"id":"` + strings.Repeat("A", 1<<20) + `"}`,
		"empty":      "",
	} {
		opts := do(h, "POST", "/auth/passkey/options", "", nil)
		rec := do(h, "POST", "/auth/passkey/finish", body, []*http.Cookie{cookie(opts, auth.PasskeyChallengeCookie)})
		if rec.Code != http.StatusUnauthorized && rec.Code != http.StatusBadRequest {
			t.Errorf("%s = %d", name, rec.Code)
		}
	}
	if got := testutil.ToFloat64(metricAdminLogins.WithLabelValues("bad_assertion")) - before; got != 4 {
		t.Errorf("%v counted as bad_assertion, want 4", got)
	}
}

func TestPasskeyCode_OnlyTheAgentAndOnlyWhilePending(t *testing.T) {
	s := withAdmin(t, fakeOperators{steve.XUID: true})
	withPasskeys(t, s)
	internal := s.InternalHandler()
	body := `{"xuid":"` + steve.XUID + `"}`
	if rec := do(internal, "POST", "/internal/v1/passkey-codes", body, nil); rec.Code != http.StatusUnauthorized {
		t.Errorf("without the token = %d", rec.Code)
	}
	if rec := do(internal, "POST", "/internal/v1/passkey-codes", body, nil, "Authorization", "Bearer "+internalToken); rec.Code != http.StatusNotFound {
		t.Errorf("nothing pending = %d", rec.Code)
	}
	if rec := do(internal, "POST", "/internal/v1/passkey-codes", `{"xuid":"<server>"}`, nil, "Authorization", "Bearer "+internalToken); rec.Code != http.StatusBadRequest {
		t.Errorf("not an XUID = %d", rec.Code)
	}
}
```

Add to the `routes` table in `service_test.go`:

```go
	"public POST /auth/passkey/options":           {"gatePublic", ""},
	"public POST /auth/passkey/finish":            {"gatePublic", ""},
	"public POST /auth/passkey/register/options":  {"gateAdmin", ""},
	"public POST /auth/passkey/register/finish":   {"gateAdmin", ""},
	"public POST /auth/passkey/register/confirm":  {"gateAdmin", ""},
	"public POST /auth/passkey/register/cancel":   {"gateAdmin", ""},
	"internal POST /internal/v1/passkey-codes":    {"gateAgent", ""},
```

- [ ] **Step 2: Run the tests and confirm they fail**

Run: `cd minecraft/mcmap && go test -count=1 -run 'TestPasskey' ./internal/server/ </dev/null`
Expected: FAIL. The routes answer 404 (or 405).

- [ ] **Step 3: Implement**

`internal/server/passkeys.go`:

```go
package server

import (
	"encoding/base64"
	"errors"
	"io"
	"net/http"
	"strings"
	"sync/atomic"

	"github.com/jdwillmsen/gameops/minecraft/mcmap/internal/auth"
)

// maxPasskeyBody is far more than any credential a browser sends, and
// small enough that a flood of them costs little to turn away.
const maxPasskeyBody = 64 << 10

// Counts since start, for the admin status.
var issuedSessions, passkeyLogins, passkeyRefused atomic.Int64

func readBody(w http.ResponseWriter, r *http.Request) ([]byte, bool) {
	b, err := io.ReadAll(http.MaxBytesReader(w, r.Body, maxPasskeyBody))
	return b, err == nil && len(b) > 0
}

func (s *Server) handlePasskeyOptions(w http.ResponseWriter, _ *http.Request) {
	opts, err := s.Ceremonies.BeginLogin(w)
	if err != nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "busy"})
		return
	}
	writeJSON(w, http.StatusOK, opts)
}

// loginRefused counts every refusal. One made before the signature is
// verified is anyone's to make and is written down at most once a minute;
// one after it is written down every time, with the XUID it was for.
func (s *Server) loginRefused(w http.ResponseWriter, result string, k *auth.Passkey) {
	metricAdminLogins.WithLabelValues(result).Inc()
	passkeyRefused.Add(1)
	if k != nil {
		s.log().Warn("map passkey login refused", "result", result, "xuid", k.XUID, "credential", auth.LogName(k.ID))
	} else if s.refusedLog.Allow(s.now()) {
		s.log().Warn("map passkey login refused", "result", result)
	}
	writeJSON(w, http.StatusUnauthorized, map[string]string{"error": result})
}

func (s *Server) handlePasskeyFinish(w http.ResponseWriter, r *http.Request) {
	body, ok := readBody(w, r)
	if !ok {
		s.loginRefused(w, auth.ResultBadAssertion, nil)
		return
	}
	k, err := s.Ceremonies.FinishLogin(w, r, body)
	var refused *auth.CeremonyError
	switch {
	case errors.As(err, &refused):
		after := refused.Result == auth.ResultPending || refused.Result == auth.ResultCounter || refused.Result == auth.ResultReplayed
		if after && k.XUID != "" {
			s.loginRefused(w, refused.Result, &k)
		} else {
			s.loginRefused(w, refused.Result, nil)
		}
		return
	case err != nil:
		s.loginRefused(w, auth.ResultBadAssertion, nil)
		return
	}
	if !s.isAdmin(r.Context(), k.XUID) {
		s.loginRefused(w, "not_operator", &k)
		return
	}
	if s.Sessions.Passkeys == nil {
		// Wiring that left sessions unable to check passkeys would issue
		// a cookie that never verifies; it shows here and not as a riddle.
		s.loginRefused(w, "disabled", &k)
		return
	}
	s.Sessions.IssuePasskey(w, auth.Identity{XUID: k.XUID, Gamertag: k.Gamertag}, auth.KeyOf(k.ID))
	metricAdminLogins.WithLabelValues("ok").Inc()
	passkeyLogins.Add(1)
	s.log().Info("map login issued", "xuid", k.XUID, "method", auth.MethodPasskey, "credential", auth.LogName(k.ID))
	writeJSON(w, http.StatusOK, map[string]string{"state": "ok", "gamertag": k.Gamertag})
}

func (s *Server) handleRegisterOptions(w http.ResponseWriter, r *http.Request) {
	sess, _ := auth.SessionFromContext(r.Context())
	var req struct {
		Label string `json:"label"`
	}
	if err := decodeStrict(w, r, &req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "label", "message": auth.ErrLabel.Error()})
		return
	}
	opts, err := s.Ceremonies.BeginRegistration(sess.XUID, sess.IssuedMilli, req.Label)
	switch {
	case errors.Is(err, auth.ErrLabel):
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "label", "message": err.Error()})
	case errors.Is(err, auth.ErrPendingExists):
		writeJSON(w, http.StatusConflict, map[string]string{"error": "pending"})
	case err != nil:
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "busy"})
	default:
		writeJSON(w, http.StatusOK, opts)
	}
}

func (s *Server) handleRegisterFinish(w http.ResponseWriter, r *http.Request) {
	sess, _ := auth.SessionFromContext(r.Context())
	body, ok := readBody(w, r)
	if !ok {
		metricPasskeysRegistered.WithLabelValues("refused").Inc()
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "refused"})
		return
	}
	k, err := s.Ceremonies.FinishRegistration(sess.Identity, sess.IssuedMilli, body)
	switch {
	case errors.Is(err, auth.ErrRegistrationExpired):
		writeJSON(w, http.StatusGone, map[string]string{"error": "expired"})
	case errors.Is(err, auth.ErrPendingExists):
		writeJSON(w, http.StatusConflict, map[string]string{"error": "pending"})
	case errors.Is(err, auth.ErrTooMany):
		writeJSON(w, http.StatusConflict, map[string]string{"error": "too_many"})
	case err != nil:
		metricPasskeysRegistered.WithLabelValues("refused").Inc()
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "refused"})
	default:
		metricPasskeysRegistered.WithLabelValues("pending").Inc()
		s.log().Info("map passkey pending", "xuid", sess.XUID, "credential", auth.LogName(k.ID))
		writeJSON(w, http.StatusOK, map[string]any{"fingerprint": auth.Fingerprint(k.ID), "expiresIn": int(auth.PendingFor.Seconds())})
	}
}

func (s *Server) handleRegisterConfirm(w http.ResponseWriter, r *http.Request) {
	sess, _ := auth.SessionFromContext(r.Context())
	var req struct {
		CredentialID string `json:"credentialId"`
		Code         string `json:"code"`
	}
	if err := decodeStrict(w, r, &req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "wrong_code"})
		return
	}
	id, err := base64.RawURLEncoding.DecodeString(strings.TrimRight(req.CredentialID, "="))
	if err != nil {
		id = nil
	}
	k, left, err := s.Passkeys.Confirm(sess.XUID, sess.IssuedMilli, id, req.Code)
	switch {
	case errors.Is(err, auth.ErrWrongCode):
		metricPasskeysRegistered.WithLabelValues("code_wrong").Inc()
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "wrong_code", "triesLeft": left})
	case errors.Is(err, auth.ErrAbandoned):
		metricPasskeysRegistered.WithLabelValues("abandoned").Inc()
		writeJSON(w, http.StatusGone, map[string]string{"error": "abandoned"})
	case errors.Is(err, auth.ErrNothingPending):
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "nothing_pending"})
	case err != nil:
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "busy"})
	default:
		metricPasskeysRegistered.WithLabelValues("confirmed").Inc()
		s.log().Info("map passkey registered", "xuid", sess.XUID, "credential", auth.LogName(k.ID))
		w.WriteHeader(http.StatusNoContent)
	}
}

func (s *Server) handleRegisterCancel(w http.ResponseWriter, r *http.Request) {
	sess, _ := auth.SessionFromContext(r.Context())
	if k, ok := s.Passkeys.Cancel(sess.XUID, sess.IssuedMilli); ok {
		metricPasskeysRegistered.WithLabelValues("cancelled").Inc()
		s.log().Info("map passkey cancelled", "xuid", sess.XUID, "credential", auth.LogName(k.ID))
	}
	w.WriteHeader(http.StatusNoContent)
}

// handlePasskeyCode is the agent asking for the code to whisper to the
// player who typed !map passkey. The XUID is the one the server stamped
// on that chat line; the code goes to that player and nobody else.
func (s *Server) handlePasskeyCode(w http.ResponseWriter, r *http.Request) {
	var req struct {
		XUID string `json:"xuid"`
	}
	if err := decodeStrict(w, r, &req); err != nil || !auth.IsXUID(req.XUID) {
		http.Error(w, "a passkey code needs the player's xuid", http.StatusBadRequest)
		return
	}
	code, fingerprint, err := s.Passkeys.IssueCode(req.XUID)
	if errors.Is(err, auth.ErrNothingPending) {
		http.Error(w, err.Error(), http.StatusNotFound)
		return
	}
	if err != nil {
		http.Error(w, "busy", http.StatusServiceUnavailable)
		return
	}
	metricPasskeysRegistered.WithLabelValues("code_issued").Inc()
	// The fingerprint is the same four bytes LogName writes.
	s.log().Info("map passkey code issued", "xuid", req.XUID, "credential", strings.ToLower(strings.ReplaceAll(fingerprint, "-", "")))
	writeJSON(w, http.StatusOK, map[string]any{"code": code, "expiresIn": int(auth.PendingFor.Seconds()), "fingerprint": fingerprint})
}
```

Add `refusedLog sparse` to `Server` beside `limitedLog`. `FinishLogin` (Task 5) returns the passkey it found alongside a refusal made after the signature, so that those log lines carry the XUID.

In `Handler`, after the `s.Sessions != nil` block:

```go
	if s.passkeysOn() {
		declareAdminMetrics()
		s.route(mux, gatePublic, "POST /auth/passkey/options", s.handlePasskeyOptions)
		s.route(mux, gatePublic, "POST /auth/passkey/finish", s.handlePasskeyFinish)
		s.route(mux, gateAdmin, "POST /auth/passkey/register/options", s.handleRegisterOptions)
		s.route(mux, gateAdmin, "POST /auth/passkey/register/finish", s.handleRegisterFinish)
		s.route(mux, gateAdmin, "POST /auth/passkey/register/confirm", s.handleRegisterConfirm)
		s.route(mux, gateAdmin, "POST /auth/passkey/register/cancel", s.handleRegisterCancel)
	}
```

In `InternalHandler`, inside the `s.Codes != nil && s.InternalToken != ""` block:

```go
		if s.passkeysOn() {
			s.route(mux, gateAgent, "POST /internal/v1/passkey-codes", s.handlePasskeyCode)
		}
```

- [ ] **Step 4: Run the package's tests and confirm they pass**

Run: `cd minecraft/mcmap && go test -count=1 -race ./internal/server/ </dev/null`
Expected: PASS, including the route-table and service-session tests.

- [ ] **Step 5: Commit**

```bash
gofmt -l minecraft/mcmap/internal/server && go vet ./minecraft/mcmap/internal/server/
/usr/bin/git add minecraft/mcmap/internal/server/passkeys.go minecraft/mcmap/internal/server/passkeys_test.go minecraft/mcmap/internal/server/server.go minecraft/mcmap/internal/server/service_test.go
/usr/bin/git commit -m "feat(mcmap): serve passkey registration, its in-game code, and passkey login" -m "Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>
Assisted-by: Claude Code:claude-opus-5-5"
```

### Task 8: The player's passkeys, and revocation

**Files:**
- Modify: `minecraft/mcmap/internal/server/passkeys.go`, `minecraft/mcmap/internal/server/server.go` (`Handler`, `InternalHandler`), `minecraft/mcmap/internal/server/auth.go` (`handleRevoke`, `handleStatus`)
- Test: `minecraft/mcmap/internal/server/passkeys_test.go`, `minecraft/mcmap/internal/server/service_test.go` (the table)

**Interfaces:**
- Produces:
  - `GET /api/passkeys` (gatePlayer) answers `200 [{"id","label","created","lastUsed","pending"}]`.
  - `DELETE /api/passkeys/{id}` (gatePlayer) answers `204` or `404`.
  - `POST /api/logout-all` (gatePlayer) answers `204` and clears the cookie.
  - `func (s *Server) revokeEverywhere(xuid string) error`: revocation plus deleting every passkey.

- [ ] **Step 1: Write the failing tests**

Append to `passkeys_test.go`:

```go
func TestPasskeys_APlayerListsAndRemovesTheirOwnOnly(t *testing.T) {
	s := withAdmin(t, fakeOperators{steve.XUID: true})
	withPasskeys(t, s)
	d := newDevice(t)
	addPasskey(t, s, session(s, steve), d)
	login := cookie(passkeyLogin(t, s, d), "__Host-mcmap_session")
	h := s.Handler()

	if rec := do(h, "GET", "/api/passkeys", "", []*http.Cookie{session(s, alex)}); strings.TrimSpace(rec.Body.String()) != "[]" {
		t.Errorf("another player's list = %s", rec.Body)
	}
	key := auth.KeyOf(d.id)
	if rec := do(h, "DELETE", "/api/passkeys/"+key, "", []*http.Cookie{session(s, alex)}); rec.Code != http.StatusNotFound {
		t.Errorf("another player removing it = %d", rec.Code)
	}
	if rec := do(h, "GET", "/api/passkeys", "", []*http.Cookie{session(s, steve)}); !strings.Contains(rec.Body.String(), key) || !strings.Contains(rec.Body.String(), "Phone") {
		t.Errorf("own list = %s", rec.Body)
	}
	// Removing it ends the session made with it, on the next request.
	if rec := do(h, "DELETE", "/api/passkeys/"+key, "", []*http.Cookie{session(s, steve)}); rec.Code != http.StatusNoContent {
		t.Fatalf("remove = %d", rec.Code)
	}
	if rec := do(h, "GET", "/api/me", "", []*http.Cookie{login}); rec.Code != http.StatusUnauthorized {
		t.Errorf("the removed passkey's session = %d", rec.Code)
	}
}

// !map logout and Log out everywhere end sessions and delete passkeys.
func TestRevocation_DeletesThatPlayersPasskeys(t *testing.T) {
	for _, via := range []string{"agent", "page"} {
		t.Run(via, func(t *testing.T) {
			s := withAdmin(t, fakeOperators{steve.XUID: true})
			withPasskeys(t, s)
			d := newDevice(t)
			addPasskey(t, s, session(s, steve), d)
			switch via {
			case "agent":
				rec := do(s.InternalHandler(), "POST", "/internal/v1/revocations", `{"xuid":"`+steve.XUID+`"}`, nil, "Authorization", "Bearer "+internalToken)
				if rec.Code != http.StatusNoContent {
					t.Fatalf("revoke = %d", rec.Code)
				}
			case "page":
				rec := do(s.Handler(), "POST", "/api/logout-all", "", []*http.Cookie{session(s, steve)})
				if rec.Code != http.StatusNoContent {
					t.Fatalf("logout-all = %d", rec.Code)
				}
			}
			if _, active := s.Passkeys.Counts(); active != 0 {
				t.Errorf("%d passkeys left after revocation", active)
			}
			if rec := passkeyLogin(t, s, d); rec.Code != http.StatusUnauthorized {
				t.Errorf("logging in with a revoked passkey = %d", rec.Code)
			}
		})
	}
}

// A game login refreshes the gamertag a passkey session will carry.
func TestPasskeys_AGameLoginRefreshesTheGamertag(t *testing.T) {
	s := withAdmin(t, fakeOperators{steve.XUID: true})
	withPasskeys(t, s)
	d := newDevice(t)
	addPasskey(t, s, session(s, steve), d)
	public, internal := s.Handler(), s.InternalHandler()
	start := do(public, "POST", "/auth/start", "", nil)
	var started struct{ Code string }
	_ = json.Unmarshal(start.Body.Bytes(), &started)
	_ = do(internal, "POST", "/internal/v1/claims", `{"code":"`+started.Code+`","xuid":"`+steve.XUID+`","gamertag":"Steve Renamed"}`, nil, "Authorization", "Bearer "+internalToken)
	_ = do(public, "GET", "/auth/status", "", []*http.Cookie{cookie(start, "__Host-mcmap_pending")})
	rec := passkeyLogin(t, s, d)
	if !strings.Contains(rec.Body.String(), "Steve Renamed") {
		t.Errorf("passkey login after a rename = %s", rec.Body)
	}
}
```

Add to the `routes` table:

```go
	"public GET /api/passkeys":                  {"gatePlayer", "/api/passkeys"},
	"public DELETE /api/passkeys/{id}":          {"gatePlayer", ""},
	"public POST /api/logout-all":               {"gatePlayer", ""},
```

- [ ] **Step 2: Run the tests and confirm they fail**

Run: `cd minecraft/mcmap && go test -count=1 -run 'TestPasskeys_|TestRevocation' ./internal/server/ </dev/null`
Expected: FAIL. The routes answer 404, and revocation leaves passkeys behind.

- [ ] **Step 3: Implement**

Append to `internal/server/passkeys.go`:

```go
func (s *Server) handleListPasskeys(w http.ResponseWriter, r *http.Request) {
	id, _ := auth.FromContext(r.Context())
	list := s.Passkeys.List(id.XUID)
	if list == nil {
		list = []auth.Listed{}
	}
	writeJSON(w, http.StatusOK, list)
}

func (s *Server) handleRemovePasskey(w http.ResponseWriter, r *http.Request) {
	id, _ := auth.FromContext(r.Context())
	k, ok := s.Passkeys.Remove(id.XUID, r.PathValue("id"))
	if !ok {
		http.NotFound(w, r)
		return
	}
	metricPasskeysRemoved.WithLabelValues("player").Inc()
	s.log().Info("map passkey removed", "xuid", id.XUID, "credential", auth.LogName(k.ID), "why", "player")
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleLogoutAll(w http.ResponseWriter, r *http.Request) {
	id, _ := auth.FromContext(r.Context())
	if err := s.revokeEverywhere(id.XUID); err != nil {
		http.Error(w, "could not record the revocation", http.StatusInternalServerError)
		return
	}
	s.Sessions.Clear(w)
	w.WriteHeader(http.StatusNoContent)
}

// revokeEverywhere is what !map logout and Log out everywhere both do: end
// every session the player was issued, and delete every passkey they hold,
// so that the way out of a stolen login is also the way out of a passkey
// someone else added.
func (s *Server) revokeEverywhere(xuid string) error {
	if err := s.Sessions.Revoked.Revoke(xuid, s.Sessions.Now()); err != nil {
		s.log().Error("map logins not revoked", "xuid", xuid, "error", err.Error())
		return err
	}
	s.log().Info("map logins revoked", "xuid", xuid)
	if s.Passkeys != nil {
		for _, k := range s.Passkeys.RemoveAll(xuid) {
			metricPasskeysRemoved.WithLabelValues("revoked").Inc()
			s.log().Info("map passkey removed", "xuid", xuid, "credential", auth.LogName(k.ID), "why", "revoked")
		}
	}
	return nil
}
```

In `auth.go`, replace the body of `handleRevoke` after the request is decoded:

```go
	if err := s.revokeEverywhere(req.XUID); err != nil {
		http.Error(w, "could not record the revocation", http.StatusInternalServerError)
		return
	}
	w.WriteHeader(http.StatusNoContent)
```

In `handleStatus`'s `auth.Claimed` case, after `s.Sessions.Issue(w, id)`:

```go
		issuedSessions.Add(1)
		if s.Passkeys != nil {
			s.Passkeys.RefreshGamertag(id.XUID, id.Gamertag)
		}
```

In `Handler`, inside the `s.Sessions != nil` block:

```go
		if s.Sessions.Revoked != nil {
			s.route(mux, gatePlayer, "POST /api/logout-all", s.handleLogoutAll)
		}
```

Inside the `s.passkeysOn()` block:

```go
		s.route(mux, gatePlayer, "GET /api/passkeys", s.handleListPasskeys)
		s.route(mux, gatePlayer, "DELETE /api/passkeys/{id}", s.handleRemovePasskey)
```

`GET /api/passkeys` and `DELETE` are on the player gate on purpose: a player who is no longer an operator can still see and remove what they added.

- [ ] **Step 4: Run the package's tests and confirm they pass**

Run: `cd minecraft/mcmap && go test -count=1 -race ./internal/server/ </dev/null`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
gofmt -l minecraft/mcmap/internal/server && go vet ./minecraft/mcmap/internal/server/
/usr/bin/git add minecraft/mcmap/internal/server/passkeys.go minecraft/mcmap/internal/server/passkeys_test.go minecraft/mcmap/internal/server/server.go minecraft/mcmap/internal/server/auth.go minecraft/mcmap/internal/server/service_test.go
/usr/bin/git commit -m "feat(mcmap): let players list and remove their passkeys, and remove them on logout everywhere" -m "Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>
Assisted-by: Claude Code:claude-opus-5-5"
```

### Task 9: The status, internal and admin

**Files:**
- Create: `minecraft/mcmap/internal/server/status.go`
- Modify: `minecraft/mcmap/internal/server/diagnostics.go` (move the build lookup into a shared function), `minecraft/mcmap/internal/server/server.go` (routes)
- Test: `minecraft/mcmap/internal/server/status_test.go` (create), `service_test.go` (table)

**Interfaces:**
- Produces `GET /internal/v1/status` (gatePublic, internal) and `GET /api/admin/status` (gateAdmin, public), with exactly the D8 fields:
  ```go
  type statusJSON struct {
      Build string `json:"build"`; Go string `json:"go"`; Now time.Time `json:"now"`
      Problem string `json:"problem"`; Quiet bool `json:"quiet"`
      Features map[string]bool `json:"features"`
      SnapshotAt *time.Time `json:"snapshotAt,omitempty"`
      RenderedAt map[string]time.Time `json:"renderedAt"`
      Assets map[string]string `json:"assets"`
      Operators string `json:"operators"`
  }
  type adminStatusJSON struct {
      statusJSON
      Live     *adminLiveJSON  `json:"live,omitempty"`  // {"lastFrameAgeSeconds": {dimension: seconds}}
      Sessions adminCountsJSON `json:"sessions"`        // {"issued","passkeyLogins","refused"}
  }
  func buildInfo() (build, goVersion string)
  ```

- [ ] **Step 1: Write the failing tests**

`internal/server/status_test.go`:

```go
package server

import (
	"encoding/json"
	"net/http"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/jdwillmsen/gameops/minecraft/mcmap/internal/auth"
	"github.com/jdwillmsen/gameops/minecraft/mcmap/internal/render"
)

var (
	hexUpTo12 = regexp.MustCompile(`^[0-9a-f]{0,12}$`)
	goVersion = regexp.MustCompile(`^go[0-9.]+`)
	rfc3339   = regexp.MustCompile(`^\d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2}(\.\d+)?Z$`)
	hex8      = regexp.MustCompile(`^[0-9a-f]{8}$`)
	featureKeys = []string{"login", "live", "markers", "waypoints", "icons", "structures", "biomes", "trails", "history", "admin", "passkeys"}
)

// checkShape fails on any key or string outside D8's table: every pod in
// the cluster can read the internal copy, so it has nowhere to put a name.
func checkShape(t *testing.T, raw []byte, admin bool) {
	t.Helper()
	var top map[string]json.RawMessage
	if err := json.Unmarshal(raw, &top); err != nil {
		t.Fatal(err)
	}
	allowed := map[string]bool{"build": true, "go": true, "now": true, "problem": true, "quiet": true, "features": true, "snapshotAt": true, "renderedAt": true, "assets": true, "operators": true}
	if admin {
		allowed["live"], allowed["sessions"] = true, true
	}
	for k := range top {
		if !allowed[k] {
			t.Errorf("a field the spec does not list: %s", k)
		}
	}
	var s struct {
		Build, Go, Now, Problem, Operators string
		SnapshotAt                         string
		Quiet                              bool
		Features                           map[string]bool
		RenderedAt, Assets                 map[string]string
		Live                               *struct {
			LastFrameAgeSeconds map[string]float64 `json:"lastFrameAgeSeconds"`
		}
		Sessions *struct{ Issued, PasskeyLogins, Refused int64 }
	}
	if err := json.Unmarshal(raw, &s); err != nil {
		t.Fatalf("%v: %s", err, raw)
	}
	if !hexUpTo12.MatchString(s.Build) || !goVersion.MatchString(s.Go) || !rfc3339.MatchString(s.Now) {
		t.Errorf("build %q go %q now %q", s.Build, s.Go, s.Now)
	}
	if s.Problem != "" && s.Problem != "snapshot" && s.Problem != "render" {
		t.Errorf("problem %q", s.Problem)
	}
	if s.Operators != "live" && s.Operators != "last_known_good" && s.Operators != "none" {
		t.Errorf("operators %q", s.Operators)
	}
	if s.SnapshotAt != "" && !rfc3339.MatchString(s.SnapshotAt) {
		t.Errorf("snapshotAt %q", s.SnapshotAt)
	}
	for k := range s.Features {
		if !strings.Contains(" "+strings.Join(featureKeys, " ")+" ", " "+k+" ") {
			t.Errorf("feature %q", k)
		}
	}
	for dim, at := range s.RenderedAt {
		if !strings.Contains(" "+strings.Join(render.Dimensions, " ")+" ", " "+dim+" ") || !rfc3339.MatchString(at) {
			t.Errorf("renderedAt %q %q", dim, at)
		}
	}
	for file, print := range s.Assets {
		if !hex8.MatchString(print) || strings.ContainsAny(file, "/\\ ") {
			t.Errorf("asset %q %q", file, print)
		}
	}
	if s.Live != nil {
		for dim := range s.Live.LastFrameAgeSeconds {
			if !strings.Contains(" "+strings.Join(render.Dimensions, " ")+" ", " "+dim+" ") {
				t.Errorf("live dimension %q", dim)
			}
		}
	}
}

func TestStatus_InternalCopyIsTheClosedShapeAndNamesNobody(t *testing.T) {
	s, _, _ := withService(t) // a server full of made-up players, markers and structures
	inGame(t, s, map[auth.Identity]int64{steve: steveInWorld})
	rec := do(s.InternalHandler(), "GET", "/internal/v1/status", "", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("internal status = %d", rec.Code)
	}
	checkShape(t, rec.Body.Bytes(), false)
	for _, never := range []string{steve.XUID, steve.Gamertag, "FWB", "http", "buried_treasure", "/data", "Mid"} {
		if strings.Contains(rec.Body.String(), never) {
			t.Errorf("the internal status holds %q: %s", never, rec.Body)
		}
	}
}

func TestStatus_AdminCopyAddsOnlyItsTwoFields(t *testing.T) {
	s, _, _ := withService(t)
	s.Operators = fakeOperators{steve.XUID: true}
	rec := do(s.Handler(), "GET", "/api/admin/status", "", []*http.Cookie{session(s, steve)})
	if rec.Code != http.StatusOK {
		t.Fatalf("admin status = %d", rec.Code)
	}
	checkShape(t, rec.Body.Bytes(), true)
	if !strings.Contains(rec.Body.String(), `"sessions"`) {
		t.Errorf("the admin status lacks sessions: %s", rec.Body)
	}
	if do(s.Handler(), "GET", "/api/admin/status", "", []*http.Cookie{session(s, alex)}).Code != http.StatusForbidden {
		t.Error("a member read the admin status")
	}
}

func TestStatus_SaysTheProblemThePageShows(t *testing.T) {
	s, _, _ := withService(t)
	s.Status.MarkProblem("render")
	s.clock = func() time.Time { return time.Now() }
	rec := do(s.InternalHandler(), "GET", "/internal/v1/status", "", nil)
	if !strings.Contains(rec.Body.String(), `"problem":"render"`) {
		t.Errorf("status = %s", rec.Body)
	}
}
```

`inGame` (in `structure_detail_test.go`) and `steveInWorld` are existing helpers of this package's tests. Import `auth` for the map type.

Add to the `routes` table:

```go
	"public GET /api/admin/status":       {"gateAdmin", "/api/admin/status"},
	"internal GET /internal/v1/status":   {"gatePublic", ""},
```

- [ ] **Step 2: Run the tests and confirm they fail**

Run: `cd minecraft/mcmap && go test -count=1 -run TestStatus ./internal/server/ </dev/null`
Expected: FAIL with 404 from both routes.

- [ ] **Step 3: Implement**

In `diagnostics.go`, move the `debug.ReadBuildInfo` block into:

```go
func buildInfo() (build, goVersion string) {
	info, ok := debug.ReadBuildInfo()
	if !ok {
		return "", ""
	}
	for _, setting := range info.Settings {
		if setting.Key == "vcs.revision" {
			build = strings.TrimSpace(setting.Value)
			if len(build) > 12 {
				build = build[:12]
			}
		}
	}
	// The revision is written to the status as hex and nothing else; a
	// build made outside version control says nothing rather than
	// something else.
	if strings.Trim(build, "0123456789abcdef") != "" {
		build = ""
	}
	return build, info.GoVersion
}
```

Change `handleDiagnostics` to use `out.Build, out.Go = buildInfo()`.

`internal/server/status.go`:

```go
package server

import (
	"net/http"
	"time"

	"github.com/jdwillmsen/gameops/minecraft/mcmap/internal/operators"
	"github.com/jdwillmsen/gameops/minecraft/mcmap/internal/render"
)

// statusJSON is what the server says about itself to whoever can reach the
// internal listener, which is every pod in the cluster while the namespace
// is not enforced. Every field is a number, a time, a boolean or a word
// from a fixed list: there is no field a name, a place or a path could be
// put in.
type statusJSON struct {
	Build      string               `json:"build"`
	Go         string               `json:"go"`
	Now        time.Time            `json:"now"`
	Problem    string               `json:"problem"`
	Quiet      bool                 `json:"quiet"`
	Features   map[string]bool      `json:"features"`
	SnapshotAt *time.Time           `json:"snapshotAt,omitempty"`
	RenderedAt map[string]time.Time `json:"renderedAt"`
	Assets     map[string]string    `json:"assets"`
	Operators  string               `json:"operators"`
}

type adminLiveJSON struct {
	LastFrameAgeSeconds map[string]float64 `json:"lastFrameAgeSeconds"`
}

type adminCountsJSON struct {
	Issued        int64 `json:"issued"`
	PasskeyLogins int64 `json:"passkeyLogins"`
	Refused       int64 `json:"refused"`
}

type adminStatusJSON struct {
	statusJSON
	Live     *adminLiveJSON  `json:"live,omitempty"`
	Sessions adminCountsJSON `json:"sessions"`
}

func (s *Server) status() statusJSON {
	snap := s.Status.Snapshot()
	now := s.now().UTC()
	out := statusJSON{
		Now: now, Problem: snap.Problem, Quiet: s.Quiet.Contains(now),
		RenderedAt: map[string]time.Time{}, Assets: s.assets(), Operators: string(operators.None),
		Features: map[string]bool{
			"login": s.Sessions != nil, "live": s.Live != nil, "markers": s.Markers != nil,
			"waypoints": s.Waypoints != nil && s.Sessions != nil, "icons": s.MobIcons != nil || s.Heads != nil || s.Art != nil,
			"structures": s.Structures != nil, "biomes": s.Biomes != nil, "trails": s.Trails != nil,
			"history": s.History != nil, "admin": s.adminOn(), "passkeys": s.passkeysOn(),
		},
	}
	out.Build, out.Go = buildInfo()
	if !snap.SnapshotAt.IsZero() {
		at := snap.SnapshotAt.UTC()
		out.SnapshotAt = &at
	}
	for _, id := range render.Dimensions {
		if at, ok := snap.RenderedAt[id]; ok {
			out.RenderedAt[id] = at.UTC()
		}
	}
	if s.adminOn() {
		out.Operators = string(s.Operators.Source())
	}
	return out
}

func (s *Server) handleInternalStatus(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, s.status())
}

func (s *Server) handleAdminStatus(w http.ResponseWriter, _ *http.Request) {
	out := adminStatusJSON{statusJSON: s.status(), Sessions: adminCountsJSON{
		Issued: issuedSessions.Load(), PasskeyLogins: passkeyLogins.Load(), Refused: passkeyRefused.Load(),
	}}
	if s.Live != nil {
		now := s.now()
		live := &adminLiveJSON{LastFrameAgeSeconds: map[string]float64{}}
		for _, id := range render.Dimensions {
			if f := s.Live.Store.Snapshot(id, now); !f.At.IsZero() {
				live.LastFrameAgeSeconds[id] = now.Sub(f.At).Seconds()
			}
		}
		out.Live = live
	}
	writeJSON(w, http.StatusOK, out)
}
```

In `InternalHandler`, after `GET /metrics`:

```go
	// Read by agents through the API server's service proxy, with no
	// credential: see statusJSON for why that is safe.
	s.route(mux, gatePublic, "GET /internal/v1/status", s.handleInternalStatus)
```

In `Handler`, in a block guarded by `s.adminOn()`:

```go
	if s.adminOn() {
		s.route(mux, gateAdmin, "GET /api/admin/status", s.handleAdminStatus)
	}
```

- [ ] **Step 4: Run the package's tests and confirm they pass**

Run: `cd minecraft/mcmap && go test -count=1 -race ./internal/server/ </dev/null`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
gofmt -l minecraft/mcmap/internal/server && go vet ./minecraft/mcmap/internal/server/
/usr/bin/git add minecraft/mcmap/internal/server/status.go minecraft/mcmap/internal/server/status_test.go minecraft/mcmap/internal/server/diagnostics.go minecraft/mcmap/internal/server/server.go minecraft/mcmap/internal/server/service_test.go
/usr/bin/git commit -m "feat(mcmap): say what the server is doing, in fixed words, on the internal listener" -m "Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>
Assisted-by: Claude Code:claude-opus-5-5"
```

### Task 10: The page: passkey login, the account panel, and the Admin section

**Files:**
- Create: `minecraft/mcmap/web/passkey.js`, `minecraft/mcmap/web/testdata/passkey.test.cjs`, `minecraft/mcmap/web/passkey_run_test.go`
- Modify: `minecraft/mcmap/web/index.html` (markup and one `<script>`), `minecraft/mcmap/web/style.css` (a few rules), `minecraft/mcmap/e2e/lib/fields.mjs` (`SCRIPTS` gains `'passkey.js'`)

**Interfaces:**
- Consumes the routes of Tasks 6 to 9: `GET api/config` (`passkeys`), `GET api/me` (`admin`), `POST auth/passkey/options`, `POST auth/passkey/finish`, `POST auth/passkey/register/{options,finish,confirm,cancel}`, `GET api/passkeys`, `DELETE api/passkeys/{id}`, `POST api/logout-all`, `GET api/admin/status`.
- Produces `window.mcmapPasskey = { labelOK, toBytes, toB64url, creationOptions, requestOptions, credentialJSON, triesText, ready }`. `ready` is the promise of the first render, for tests.
- Element ids: `login-passkey`, `login-passkey-note`, `account-open`, `account`, `account-passkeys`, `account-logout-all`, `account-note`, `admin`, `admin-status`, `admin-label`, `admin-add`, `admin-step`, `admin-fingerprint`, `admin-code`, `admin-confirm`, `admin-cancel`, `admin-note`.

- [ ] **Step 1: Write the failing page test**

`web/testdata/passkey.test.cjs`:

```js
'use strict';

// The passkey login, the account panel and the Admin section, run and not
// read, against a server and an authenticator made of plain functions. Run
// by `go test ./web/` where there is a node, or by hand:
// `node web/testdata/passkey.test.cjs`.
const assert = require('node:assert/strict');
const vm = require('node:vm');
const fs = require('node:fs');
const path = require('node:path');
const { page } = require('./dom.cjs');

const tests = [];
const test = (name, fn) => tests.push([name, fn]);
const source = (file) => fs.readFileSync(path.join(__dirname, '..', file), 'utf8');
const tick = () => new Promise((done) => setTimeout(done, 0));

const json = (status, body) => ({ ok: status < 300, status, json: async () => body });

// A page with passkey.js in it. answers maps "METHOD path" to a function of
// the request body, or to a response; webauthn says whether the browser has
// it and what the authenticator answers; signal says whether the browser
// can be asked to drop a credential.
function env({ answers = {}, webauthn = true, signal = true, confirm = true } = {}) {
  const { win, doc } = page(new Map(), { files: [] });
  const made = new Map();
  const plain = doc.getElementById;
  const kind = (id) => (/-(passkey|open|logout-all|add|confirm|cancel)$/.test(id) ? 'button' : /-(label|code)$/.test(id) ? 'input' : /passkeys$/.test(id) ? 'ul' : 'div');
  doc.getElementById = (id) => plain(id) || (made.has(id) ? made.get(id) : (made.set(id, Object.assign(doc.createElement(kind(id)), { id })), made.get(id)));
  for (const id of ['login-passkey', 'account-open', 'account', 'admin']) doc.getElementById(id).hidden = true;
  const calls = [];
  const signalled = [];
  win.location = { hostname: 'map.example', reload() { calls.push('reload'); } };
  win.confirm = (text) => { calls.push(`confirm ${text}`); return confirm; };
  win.fetch = async (url, init = {}) => {
    const key = `${init.method || 'GET'} ${url}`;
    calls.push(key);
    const answer = answers[key];
    if (typeof answer === 'function') return answer(init.body ? JSON.parse(init.body) : null);
    return answer || json(404, {});
  };
  if (webauthn) {
    win.PublicKeyCredential = function PublicKeyCredential() {};
    if (signal) win.PublicKeyCredential.signalUnknownCredential = async (o) => { signalled.push(o); };
    const cred = (response) => ({ id: 'Y3JlZA', rawId: new Uint8Array([99, 114, 101, 100]).buffer, type: 'public-key', response });
    win.navigator = {
      credentials: {
        create: async () => cred({ clientDataJSON: new Uint8Array([1]).buffer, attestationObject: new Uint8Array([2]).buffer }),
        get: async () => cred({ clientDataJSON: new Uint8Array([1]).buffer, authenticatorData: new Uint8Array([3]).buffer, signature: new Uint8Array([4]).buffer, userHandle: new Uint8Array([5]).buffer }),
      },
    };
  } else {
    win.navigator = {};
  }
  // page() has already made win a context; these are Node's, which a
  // fresh context does not have.
  win.atob = atob;
  win.btoa = btoa;
  vm.runInContext(source('passkey.js'), win, { filename: 'passkey.js' });
  return { win, doc, calls, signalled, el: (id) => doc.getElementById(id) };
}

const options = { publicKey: { challenge: 'AAAA', user: { id: 'AQID', name: 'fwb-map', displayName: 'FWB map admin' }, excludeCredentials: [{ id: 'BAUG', type: 'public-key' }] } };

test('the model: labels, bytes and tries', () => {
  const { labelOK, toBytes, toB64url, triesText } = env().win.mcmapPasskey;
  for (const good of ['Phone', 'Work laptop 2', 'Key-A']) assert.ok(labelOK(good), good);
  for (const bad of ['', '   ', 'Café', 'Phone 📱', 'a'.repeat(33), '<b>x</b>']) assert.ok(!labelOK(bad), bad);
  assert.equal(toB64url(toBytes('AQID-_8')), 'AQID-_8');
  assert.equal(triesText(1), '1 try left');
  assert.equal(triesText(2), '2 tries left');
});

test('the passkey login is offered only when the server and the browser both have it', async () => {
  const on = env({ answers: { 'GET api/config': json(200, { login: true, passkeys: true }) } });
  await on.win.mcmapPasskey.ready;
  assert.equal(on.el('login-passkey').hidden, false);
  const off = env({ answers: { 'GET api/config': json(200, { login: true, passkeys: false }) } });
  await off.win.mcmapPasskey.ready;
  assert.equal(off.el('login-passkey').hidden, true);
  const old = env({ webauthn: false, answers: { 'GET api/config': json(200, { login: true, passkeys: true }) } });
  await old.win.mcmapPasskey.ready;
  assert.equal(old.el('login-passkey').hidden, true, 'a browser with no WebAuthn keeps the code login only');
});

test('logging in with a passkey sends what the authenticator signed and reloads', async () => {
  const sent = [];
  const e = env({ answers: {
    'GET api/config': json(200, { passkeys: true }),
    'POST auth/passkey/options': json(200, options),
    'POST auth/passkey/finish': (body) => { sent.push(body); return json(200, { state: 'ok' }); },
  } });
  await e.win.mcmapPasskey.ready;
  e.el('login-passkey').click();
  for (let i = 0; i < 5; i++) await tick();
  assert.equal(sent.length, 1);
  assert.equal(sent[0].response.userHandle, 'BQ');
  assert.ok(e.calls.includes('reload'));
});

test('the Admin section is there for an admin only', async () => {
  const member = env({ answers: { 'GET api/me': json(200, { gamertag: 'Steve Builds' }), 'GET api/passkeys': json(200, []) } });
  await member.win.mcmapPasskey.ready;
  assert.equal(member.el('admin').hidden, true);
  assert.ok(!member.calls.includes('GET api/admin/status'));
  const admin = env({ answers: {
    'GET api/me': json(200, { gamertag: 'Steve Builds', admin: true }),
    'GET api/passkeys': json(200, []),
    'GET api/admin/status': json(200, { build: 'abc', problem: '', operators: 'live', features: { admin: true } }),
  } });
  await admin.win.mcmapPasskey.ready;
  assert.equal(admin.el('admin').hidden, false);
  assert.match(admin.el('admin-status').textContent, /live/);
});

function adding(extra = {}) {
  return env({ ...extra, answers: {
    'GET api/me': json(200, { gamertag: 'Steve Builds', admin: true }),
    'GET api/passkeys': json(200, []),
    'GET api/admin/status': json(200, {}),
    'POST auth/passkey/register/options': json(200, options),
    'POST auth/passkey/register/finish': json(200, { fingerprint: '7F3A-91C2', expiresIn: 120 }),
    'POST auth/passkey/register/cancel': json(204, {}),
    ...extra.answers,
  } });
}

async function startAdding(e, label = 'Phone') {
  await e.win.mcmapPasskey.ready;
  e.el('admin-label').value = label;
  e.el('admin-add').click();
  for (let i = 0; i < 5; i++) await tick();
}

test('adding a passkey asks for !map passkey and shows the fingerprint to compare', async () => {
  const e = adding();
  await startAdding(e);
  assert.match(e.el('admin-step').textContent, /!map passkey/);
  assert.equal(e.el('admin-fingerprint').textContent, '7F3A-91C2');
  assert.equal(e.el('admin-code').hidden, false);
});

test('a label outside the rules is refused before anything is asked of the server', async () => {
  const e = adding();
  await startAdding(e, 'Café');
  assert.ok(!e.calls.includes('POST auth/passkey/register/options'));
  assert.match(e.el('admin-note').textContent, /1 to 32 letters, digits, spaces or hyphens/);
});

test('a wrong code says how many tries are left, and the third starts again and drops the orphan', async () => {
  let wrong = 0;
  const e = adding({ answers: { 'POST auth/passkey/register/confirm': () => (++wrong < 3 ? json(400, { error: 'wrong_code', triesLeft: 3 - wrong }) : json(410, { error: 'abandoned' })) } });
  await startAdding(e);
  for (const want of [/2 tries left/, /1 try left/, /start again/i]) {
    e.el('admin-code').value = 'ZZZZZZ';
    e.el('admin-confirm').click();
    for (let i = 0; i < 5; i++) await tick();
    assert.match(e.el('admin-note').textContent, want);
  }
  assert.equal(e.signalled.length, 1);
  assert.equal(e.signalled[0].rpId, 'map.example');
});

test('with no signal API the page says a passkey may be left behind', async () => {
  const e = adding({ signal: false, answers: { 'POST auth/passkey/register/confirm': json(404, { error: 'nothing_pending' }) } });
  await startAdding(e);
  e.el('admin-code').value = 'ABC234';
  e.el('admin-confirm').click();
  for (let i = 0; i < 5; i++) await tick();
  assert.match(e.el('admin-note').textContent, /password manager/);
});

test('the confirm request carries the credential this page created', async () => {
  const sent = [];
  const e = adding({ answers: { 'POST auth/passkey/register/confirm': (body) => { sent.push(body); return json(204, {}); } } });
  await startAdding(e);
  e.el('admin-code').value = 'abc234';
  e.el('admin-confirm').click();
  for (let i = 0; i < 5; i++) await tick();
  assert.deepEqual(JSON.parse(JSON.stringify(sent)), [{ credentialId: 'Y3JlZA', code: 'ABC234' }]);
  assert.match(e.el('admin-note').textContent, /added/);
});

test('a label is shown as text and never as markup', async () => {
  const e = env({ answers: { 'GET api/me': json(200, { gamertag: 'Steve Builds' }), 'GET api/passkeys': json(200, [{ id: 'k1', label: '<b>x</b>', created: '2026-10-14T00:00:00Z' }]) } });
  await e.win.mcmapPasskey.ready;
  const list = e.el('account-passkeys');
  assert.equal(list.children.length, 1);
  assert.match(list.children[0].textContent, /<b>x<\/b>/);
});

test('Log out everywhere asks first and says passkeys go too', async () => {
  const e = env({ answers: { 'GET api/me': json(200, { gamertag: 'Steve Builds' }), 'GET api/passkeys': json(200, []), 'POST api/logout-all': json(204, {}) } });
  await e.win.mcmapPasskey.ready;
  e.el('account-logout-all').click();
  for (let i = 0; i < 5; i++) await tick();
  const asked = e.calls.find((c) => c.startsWith('confirm '));
  assert.equal(asked, 'confirm This logs out every browser logged in as you and removes all your passkeys.');
  assert.ok(e.calls.includes('POST api/logout-all'));
  const no = env({ confirm: false, answers: { 'GET api/me': json(200, { gamertag: 'Steve Builds' }), 'GET api/passkeys': json(200, []) } });
  await no.win.mcmapPasskey.ready;
  no.el('account-logout-all').click();
  await tick();
  assert.ok(!no.calls.includes('POST api/logout-all'));
});

async function main() {
  let failed = 0;
  for (const [name, fn] of tests) {
    try {
      await fn();
      console.log(`ok   ${name}`);
    } catch (err) {
      failed += 1;
      console.log(`FAIL ${name}\n     ${String(err && err.stack ? err.stack : err).split('\n').slice(0, 6).join('\n     ')}`);
    }
  }
  console.log(`${tests.length - failed} of ${tests.length} passed`);
  process.exitCode = failed === 0 ? 0 : 1;
}

main();
```

`web/passkey_run_test.go`:

```go
package web

import (
	"os/exec"
	"strings"
	"testing"
)

// The passkey login and the account and Admin panels are run, not read:
// passkey.js in a page of plain objects against a stub server and a stub
// authenticator. Skipped where there is no node to run it with.
func TestPasskeyPageDoesWhatItSaysWhenRun(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("no node on this machine to run the passkey script with")
	}
	out, err := exec.Command(node, "testdata/passkey.test.cjs").CombinedOutput()
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		if !strings.HasPrefix(line, "ok ") {
			t.Log(line)
		}
	}
	if err != nil {
		t.Errorf("the passkey script did not do what is asked of it: %v", err)
	}
}
```

- [ ] **Step 2: Run the test and confirm it fails**

Run: `cd minecraft/mcmap && go test -count=1 -run TestPasskeyPage ./web/ </dev/null`
Expected: FAIL, `ENOENT … passkey.js`.

- [ ] **Step 3: Implement `web/passkey.js`**

```js
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
    if (!ul) return;
    ul.replaceChildren(...list.map((k) => {
      const li = document.createElement('li');
      li.textContent = `${k.label}${k.pending ? ' (waiting for the code)' : ''}`;
      const remove = document.createElement('button');
      remove.type = 'button';
      remove.textContent = 'Remove';
      remove.addEventListener('click', async () => {
        await fetch(`api/passkeys/${encodeURIComponent(k.id)}`, { method: 'DELETE' });
        li.remove();
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
    $('admin-add').addEventListener('click', async () => {
      const label = $('admin-label').value;
      if (!labelOK(label)) { say('admin-note', LABEL_RULE); return; }
      say('admin-note', '');
      const opts = await post('auth/passkey/register/options', { label });
      if (!opts.ok) {
        const why = (await opts.json()).error;
        say('admin-note', why === 'pending' ? 'A passkey is already waiting for its code. Finish or cancel it first.' : why === 'label' ? LABEL_RULE : 'The map could not start that. Try again.');
        return;
      }
      let cred;
      try { cred = await navigator.credentials.create(creationOptions(await opts.json())); } catch { say('admin-note', 'No passkey was made.'); return; }
      const fin = await post('auth/passkey/register/finish', credentialJSON(cred));
      if (!fin.ok) { say('admin-note', 'The map did not accept that passkey.'); await signalOrphan(cred.id); return; }
      created = cred.id;
      const { fingerprint } = await fin.json();
      say('admin-fingerprint', fingerprint);
      say('admin-step', `Now type !map passkey in the game. You will be whispered a code to type here. Check that the whisper shows the same fingerprint, ${fingerprint}.`);
      $('admin-code').hidden = false;
      $('admin-confirm').hidden = false;
      $('admin-cancel').hidden = false;
    });
    $('admin-confirm').addEventListener('click', async () => {
      const code = String($('admin-code').value || '').trim().toUpperCase();
      const res = await post('auth/passkey/register/confirm', { credentialId: created, code });
      if (res.status === 204) { say('admin-note', 'Passkey added.'); created = null; return; }
      const body = await res.json();
      if (body.error === 'wrong_code') { say('admin-note', `That is not the code. ${triesText(body.triesLeft)}.`); return; }
      const orphan = created;
      created = null;
      say('admin-note', 'Start again.');
      await signalOrphan(orphan);
    });
    $('admin-cancel').addEventListener('click', async () => {
      await post('auth/passkey/register/cancel');
      if (created) await signalOrphan(created);
      created = null;
    });
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
      await post('api/logout-all');
      window.location.reload();
    });
    try { listPasskeys(await (await fetch('api/passkeys', { cache: 'no-store' })).json()); } catch { /* a map with passkeys off lists none */ }
    if (!me.admin) return;
    $('admin').hidden = false;
    adminSection();
    try { showStatus(await (await fetch('api/admin/status', { cache: 'no-store' })).json()); } catch { /* the section still offers adding a passkey */ }
  }

  api.ready = Promise.all([loginButton(), account()]);
})();
```

In `index.html`:

- Inside `#login`, after `login-new`, add `<button id="login-passkey" type="button" hidden>Log in with a passkey</button>` and `<p id="login-passkey-note" class="note" role="status"></p>`.
- After `<span id="who" …>`, add `<button id="account-open" type="button" hidden>Account</button>`.
- Before the debug `<section>`, add:

```html
  <section id="account" class="account" aria-label="Account" hidden>
    <h2>Your passkeys</h2>
    <ul id="account-passkeys"></ul>
    <button id="account-logout-all" type="button">Log out everywhere</button>
    <p id="account-note" class="note" role="status"></p>
    <div id="admin" class="admin" hidden>
      <h2>Admin</h2>
      <div id="admin-status"></div>
      <label>Name for a new passkey <input id="admin-label" maxlength="32" autocomplete="off"></label>
      <button id="admin-add" type="button">Add a passkey</button>
      <p id="admin-step" class="note"></p>
      <code id="admin-fingerprint"></code>
      <input id="admin-code" maxlength="6" autocomplete="one-time-code" hidden>
      <button id="admin-confirm" type="button" hidden>Confirm</button>
      <button id="admin-cancel" type="button" hidden>Cancel</button>
      <p id="admin-note" class="note" role="status"></p>
    </div>
  </section>
```

- After `<script src="appearance.js"></script>` and the other scripts, add `<script src="passkey.js"></script>`.

In `style.css`, give `.account` the same panel look as `.debug`. Copy the `.debug` block's position, background, border and padding rules under `.account`, and add `.account[hidden], .admin[hidden] { display: none; }`. Do not touch the CSP: the page sets no inline style or script.

In `e2e/lib/fields.mjs`, add `'passkey.js'` to `SCRIPTS`, before `'leaflet.js'`.

- [ ] **Step 4: Run the page tests and confirm they pass**

Run: `cd minecraft/mcmap && go test -count=1 -race ./web/ </dev/null`
Expected: PASS, including `TestEveryAssetThePageLoadsIsEmbedded`, which now sees `passkey.js`.

- [ ] **Step 5: Commit**

```bash
/usr/bin/git add minecraft/mcmap/web/passkey.js minecraft/mcmap/web/passkey_run_test.go minecraft/mcmap/web/testdata/passkey.test.cjs minecraft/mcmap/web/index.html minecraft/mcmap/web/style.css minecraft/mcmap/e2e/lib/fields.mjs
/usr/bin/git commit -m "feat(mcmap): add passkey login, the account panel and the Admin section to the page" -m "Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>
Assisted-by: Claude Code:claude-opus-5-5"
```

### Task 11: Wiring, the e2e harness, and the README

**Files:**
- Modify: `minecraft/mcmap/cmd/mcmap/main.go`, `minecraft/mcmap/cmd/mcmap/main_test.go`, `minecraft/mcmap/e2e/harness/main.go`, `minecraft/mcmap/e2e/checks/access.mjs`, `minecraft/mcmap/e2e/lib/fields.mjs`, `minecraft/mcmap/README.md`

**Interfaces:**
- Consumes everything above.
- Produces a running map in which `ADMIN_ENABLED` and `PASSKEYS_ENABLED` do what the spec says, and an e2e run that proves a service session is refused by every admin and passkey route.

- [ ] **Step 1: Write the failing e2e check**

In `e2e/checks/access.mjs`, extend `scope` to ask the admin and passkey routes as well. Add these to the object `page.evaluate` returns:

```js
      adminStatus: (await fetch('api/admin/status', { cache: 'no-store' })).status,
      passkeys: (await fetch('api/passkeys', { cache: 'no-store' })).status,
```

After the waypoints check, add:

```js
  if (got.adminStatus !== 403 && got.adminStatus !== 404) throw new Fail('an admin route did not refuse a service session', { admin_status: got.adminStatus });
  if (got.passkeys !== 403 && got.passkeys !== 404) throw new Fail('the passkey list did not refuse a service session', { passkeys_status: got.passkeys });
```

and return `{ measures: { waypoints_status: got.waypoints, admin_status: got.adminStatus, passkeys_status: got.passkeys } }`.

In `e2e/lib/fields.mjs`, add the two sentences `"an admin route did not refuse a service session"` and `"the passkey list did not refuse a service session"` to `SENTENCES`, and `'admin_status'`, `'passkeys_status'` to `MEASURES`. Only reads are made, so the suite's read-only rule holds.

In `e2e/harness/main.go`, in `run`, after the `if cfg.ServiceSecret != "" { … }` block, turn admin and passkeys on. Use a fake operator list holding one made-up XUID that no session the suite holds ever is:

```go
	// One made-up operator, so that the admin and passkey routes are
	// served and every session the suite holds is refused by them. The
	// suite never completes a ceremony, so the origin only has to parse.
	app.Admin, app.Operators = true, harnessOperators{"2535412345678909": true}
	keys, err := auth.LoadPasskeys(filepath.Join(dir, "passkeys.json"), time.Now)
	if err != nil {
		return err
	}
	cer, err := auth.NewCeremonies("https://localhost", "localhost", key, keys, time.Now)
	if err != nil {
		return err
	}
	app.Passkeys, app.Ceremonies = keys, cer
	app.Sessions.Passkeys, app.Sessions.PasskeyTTL = keys, 12*time.Hour
```

Add the type at the end of the file, and add `"github.com/jdwillmsen/gameops/minecraft/mcmap/internal/operators"` to the imports:

```go
type harnessOperators map[string]bool

func (h harnessOperators) IsOperator(_ context.Context, xuid string) (bool, operators.Source) {
	return h[xuid], operators.Live
}
func (harnessOperators) Source() operators.Source { return operators.Live }
```

- [ ] **Step 2: Run the e2e suite and confirm the new check is exercised**

Run: `minecraft/mcmap/e2e/local.sh`
Expected: before the harness change the `scope` check fails with `an_admin_route_did_not_refuse_a_service_session` only if a route answers 200. With the harness change it passes, reporting `admin_status=403`.

- [ ] **Step 3: Wire the service**

In `cmd/mcmap/main.go`, inside `if cfg.Login { … }` after the service-session block:

```go
		if cfg.Admin {
			list, err := operators.Open(filepath.Join(cfg.DataDir, "auth", "operators.json"), operators.NewBridge(cfg.BridgeURL, cfg.BridgeToken), time.Now, logger)
			if err != nil {
				return err
			}
			wg.Add(1)
			go func() { defer wg.Done(); list.Run(ctx) }()
			app.Admin, app.Operators = true, list
		}
		if cfg.Passkeys {
			keys, err := auth.LoadPasskeys(filepath.Join(cfg.DataDir, "auth", "passkeys.json"), time.Now)
			if err != nil {
				return err
			}
			keys.Counted, keys.Expired = server.CountPasskeys, server.CountPasskeyExpired
			pending, active := keys.Counts()
			server.CountPasskeys(pending, active)
			cer, err := auth.NewCeremonies(cfg.PublicOrigin, cfg.PasskeyRPID, key, keys, time.Now)
			if err != nil {
				return err
			}
			app.Passkeys, app.Ceremonies = keys, cer
			app.Sessions.Passkeys, app.Sessions.PasskeyTTL = keys, 12*time.Hour
		}
```

Set `Quiet: cfg.Quiet` in the `server.Server{…}` literal. Add `"admin", cfg.Admin, "passkeys", cfg.Passkeys` to the `starting` log line. When `cfg.Login` is false and either switch was asked for, log once: `logger.Warn("admin and passkeys asked for with no login: there is no player to be an admin")`. `config.Load` has already turned both off, so read the raw environment for this one line: `os.Getenv("ADMIN_ENABLED") == "true"`.

Add to `cmd/mcmap/main_test.go`, following the file's existing style of building the service from an environment and probing its handlers:

```go
// With the switches off, which is the default, none of the new routes is
// served and nobody is an admin.
func TestRun_AdminAndPasskeysAreOffByDefault(t *testing.T) {
	cfg, err := config.Load(func(k string) string {
		return map[string]string{"BRIDGE_URL": "http://bridge.invalid", "BRIDGE_TOKEN": "tok", "LEVEL_NAME": "FWB", "INTERNAL_TOKEN": "internal-token-0123456789"}[k]
	})
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Admin || cfg.Passkeys {
		t.Fatalf("defaults: admin %v passkeys %v", cfg.Admin, cfg.Passkeys)
	}
}
```

- [ ] **Step 4: Document it in the README**

In `minecraft/mcmap/README.md`:

- Add a section `## Admin access` after `## Service sessions`. It says, in the file's voice: who an admin is (D1, with the 10 s cache and the day of last-known-good); how a passkey is added (the six steps of D3, the fingerprint, three tries); how one logs in; what an admin sees (D5); how to take it all back (D7's table, both switches in one change to turn admin off); and that agents read `/internal/v1/status` through the API server's service proxy, with the `kubectl get --raw` line from D8.
- In `## Environment variables`, add rows for `ADMIN_ENABLED` (default `false`), `PASSKEYS_ENABLED` (default `false`, needs `ADMIN_ENABLED`) and `PUBLIC_ORIGIN` (required with passkeys).
- In `## Endpoints`, add every route from Tasks 7 to 9 with its gate.
- In `## Metrics`, add every metric from D6 with its labels.

No real gamertag, XUID or host other than `fwb.prd.jdwlabs.com`, which is already public in the chart, goes in.

- [ ] **Step 5: Run everything and confirm it passes**

```bash
cd minecraft/mcmap && go test -count=1 -race ./... </dev/null
cd ../.. && test -z "$(gofmt -l minecraft/mcmap)" && go vet ./minecraft/mcmap/...
minecraft/mcmap/e2e/local.sh
/usr/bin/git status --short | grep -v '^??' ; /usr/bin/git status --short | grep 'minecraft/mcmap/mcmap$' && echo "STOP: the build output is in the tree"
```

Expected: all pass, and no `minecraft/mcmap/mcmap` binary listed.

- [ ] **Step 6: Commit, push and open the group's pull request**

```bash
/usr/bin/git add minecraft/mcmap/cmd/mcmap/main.go minecraft/mcmap/cmd/mcmap/main_test.go minecraft/mcmap/e2e/harness/main.go minecraft/mcmap/e2e/checks/access.mjs minecraft/mcmap/e2e/lib/fields.mjs minecraft/mcmap/README.md
/usr/bin/git commit -m "feat(mcmap): wire admin access and passkeys behind their switches, and document them" -m "Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>
Assisted-by: Claude Code:claude-opus-5-5"
```

Ship it through the user's normal path (`/no-mistakes`, or `git push -u origin feat/map-admin-access-map` and a PR). The PR body follows the global rules: Why, Needs attention, Verified, about 150 words, no attribution footer. The points that need attention are `internal/auth/ceremony.go` (the checks made beside the library), `internal/server/admin.go` (the gate) and `internal/server/status.go` (the closed shape). Merging releases a new `minecraft-map` image with both switches off, so production does not change until Group 3.

---

## Group 2: the agent's `!map passkey` (`gameops`, `feat/map-admin-access-agent`)

Every task here is in `minecraft/agent`. Run its tests from there:

```bash
cd minecraft/agent && go test -count=1 -race ./... </dev/null
```

The map's endpoint, as Task 7 makes it: `POST /internal/v1/passkey-codes` takes `{"xuid":"<digits>"}` with the internal token. It answers `200 {"code":"ABC234","expiresIn":120,"fingerprint":"7F3A-91C2"}` or `404` when nothing is pending.

### Task 12: `!map passkey`, the whisper, and the new `!map logout` reply

**Files:**
- Modify: `minecraft/agent/internal/mapclient/client.go`, `minecraft/agent/internal/plugins/maplogin.go`
- Test: `minecraft/agent/internal/mapclient/client_test.go`, `minecraft/agent/internal/plugins/maplogin_test.go`

**Interfaces:**
- Produces:
  ```go
  // mapclient
  var ErrNoPendingPasskey = errors.New("map: no passkey is waiting for that player")
  func (c *Client) PasskeyCode(ctx context.Context, xuid string) (code, fingerprint string, err error)
  // plugins
  type MapClaimer interface {
      Claim(ctx context.Context, code, xuid, gamertag string) error
      Revoke(ctx context.Context, xuid string) error
      PasskeyCode(ctx context.Context, xuid string) (code, fingerprint string, err error)
  }
  ```
  The `map` command gains `RedactReply: true`.

- [ ] **Step 1: Write the failing tests**

Append to `internal/mapclient/client_test.go`:

```go
func TestPasskeyCode_AsksForTheSendersCodeAndReadsIt(t *testing.T) {
	var seen http.Request
	var body string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seen = *r
		raw, _ := io.ReadAll(r.Body)
		body = string(raw)
		_, _ = w.Write([]byte(`{"code":"ABC234","expiresIn":120,"fingerprint":"7F3A-91C2"}`))
	}))
	defer srv.Close()
	code, fp, err := New(srv.URL, "internal-token", time.Second).PasskeyCode(context.Background(), "2535412345678901")
	if err != nil || code != "ABC234" || fp != "7F3A-91C2" {
		t.Fatalf("PasskeyCode = %q %q %v", code, fp, err)
	}
	if seen.URL.Path != "/internal/v1/passkey-codes" || seen.Header.Get("Authorization") != "Bearer internal-token" || body != `{"xuid":"2535412345678901"}` {
		t.Errorf("request %s %q %s", seen.URL.Path, seen.Header.Get("Authorization"), body)
	}
}

func TestPasskeyCode_NothingPendingIsItsOwnErrorAndOtherFailuresQuoteNoBody(t *testing.T) {
	var seen http.Request
	var body string
	if _, _, err := server(t, http.StatusNotFound, &seen, &body).PasskeyCode(context.Background(), "2535412345678901"); !errors.Is(err, ErrNoPendingPasskey) {
		t.Errorf("404 = %v", err)
	}
	_, _, err := server(t, http.StatusServiceUnavailable, &seen, &body).PasskeyCode(context.Background(), "2535412345678901")
	if err == nil || strings.Contains(err.Error(), "detail") {
		t.Errorf("503 = %v; want an error that quotes nothing the map said", err)
	}
}

func TestPasskeyCode_AnAnswerOfTheWrongShapeIsAnError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"code":"<script>","fingerprint":"x"}`))
	}))
	defer srv.Close()
	if _, _, err := New(srv.URL, "t", time.Second).PasskeyCode(context.Background(), "2535412345678901"); err == nil {
		t.Error("a code of the wrong shape was passed on to a player")
	}
}
```

In `internal/plugins/maplogin_test.go`, give `fakeClaimer` the new method and fields:

```go
	passkeyCode, passkeyFP string
	passkeyErr             error
	passkeyFor             []string
```

```go
func (f *fakeClaimer) PasskeyCode(_ context.Context, xuid string) (string, string, error) {
	f.passkeyFor = append(f.passkeyFor, xuid)
	return f.passkeyCode, f.passkeyFP, f.passkeyErr
}
```

Then add:

```go
const passkeyWhisper = "Your map passkey code is ABC234, for the passkey with fingerprint 7F3A-91C2. Type it into the map page that asked for it, within 2 minutes, only if that page shows the same fingerprint. It lets that browser log in to the map as you, as admin, without the game. Never give it to anyone, and never type it into a page someone else sent you. If you did not just add a passkey, type !map logout."

func TestMap_PasskeyWhispersTheCodeWithItsWarning(t *testing.T) {
	for _, word := range []string{"passkey", "PASSKEY"} {
		claimer := &fakeClaimer{passkeyCode: "ABC234", passkeyFP: "7F3A-91C2"}
		reply, err := runMap(t, claimer, word)
		if err != nil {
			t.Fatal(err)
		}
		if reply != passkeyWhisper {
			t.Errorf("!map %s reply = %q", word, reply)
		}
		if len(claimer.passkeyFor) != 1 || claimer.passkeyFor[0] != "2535412345678901" || claimer.calls != 0 {
			t.Errorf("asked for %v, claims %d", claimer.passkeyFor, claimer.calls)
		}
	}
}

func TestMap_PasskeyWithNothingWaitingSaysSo(t *testing.T) {
	reply, err := runMap(t, &fakeClaimer{passkeyErr: mapclient.ErrNoPendingPasskey}, "passkey")
	if err != nil || reply != "There is no passkey waiting to be added for you. Start from the map page." {
		t.Errorf("reply %q, err %v", reply, err)
	}
}

func TestMap_PasskeyIsNotTheConsoles(t *testing.T) {
	p := NewMapLogin(&fakeClaimer{passkeyCode: "ABC234"}, fakeNames{}, mapURL)
	reply, _ := p.Commands()[0].Run(context.Background(), &plugin.Context{}, plugin.Invocation{ActorXUID: chat.ServerOrigin, Args: []string{"passkey"}})
	if strings.Contains(reply, "ABC234") {
		t.Errorf("the console was given a code: %q", reply)
	}
}

func TestMap_RepliesAreKeptOutOfTheLog(t *testing.T) {
	for _, c := range NewMapLogin(&fakeClaimer{}, fakeNames{}, mapURL).Commands() {
		if c.Name == "map" && !c.RedactReply {
			t.Error("!map's replies, which now carry passkey codes, are logged")
		}
	}
}
```

In `TestMap_LogoutEndsEveryMapLoginOfWhoeverTypedIt`, change the reply check to the new text exactly:

```go
		if reply != "Every browser that was logged in to the map as you has been logged out, and any passkeys for your map login have been removed." {
			t.Errorf("reply = %q", reply)
		}
```

- [ ] **Step 2: Run the tests and confirm they fail**

Run: `cd minecraft/agent && go test -count=1 ./internal/mapclient/ ./internal/plugins/ </dev/null`
Expected: FAIL to compile, `undefined: ErrNoPendingPasskey`.

- [ ] **Step 3: Implement**

In `internal/mapclient/client.go`:

```go
// ErrNoPendingPasskey is the map saying nobody started adding a passkey
// for that player in the last two minutes.
var ErrNoPendingPasskey = errors.New("map: no passkey is waiting for that player")

var (
	passkeyCodeShape   = regexp.MustCompile(`^[A-HJ-NP-Z2-9]{6}$`)
	passkeyPrintShape  = regexp.MustCompile(`^[0-9A-F]{4}-[0-9A-F]{4}$`)
)

// PasskeyCode asks the map for the code to whisper to the player who typed
// !map passkey. A failure quotes nothing the map said: on this path the
// map's words are a code meant for one player.
func (c *Client) PasskeyCode(ctx context.Context, xuid string) (code, fingerprint string, err error) {
	body, err := json.Marshal(map[string]string{"xuid": xuid})
	if err != nil {
		return "", "", err
	}
	status, reply, err := c.do(ctx, http.MethodPost, "/internal/v1/passkey-codes", body)
	switch {
	case err != nil:
		return "", "", fmt.Errorf("map: passkey code: %w", err)
	case status == http.StatusNotFound:
		return "", "", ErrNoPendingPasskey
	case status != http.StatusOK:
		return "", "", fmt.Errorf("map: passkey code: status %d", status)
	}
	var out struct {
		Code        string `json:"code"`
		Fingerprint string `json:"fingerprint"`
	}
	if err := json.Unmarshal(reply, &out); err != nil || !passkeyCodeShape.MatchString(out.Code) || !passkeyPrintShape.MatchString(out.Fingerprint) {
		return "", "", errors.New("map: passkey code: an answer of the wrong shape")
	}
	return out.Code, out.Fingerprint, nil
}
```

Add `"regexp"` to the imports.

In `internal/plugins/maplogin.go`, add `PasskeyCode` to `MapClaimer` as in Interfaces. Set `RedactReply: true` on the `map` command, with the comment `// A reply can be a passkey code meant for one player.`. Update the description to `"Unlock the world map in your browser: !map <code>. !map logout ends your map logins. !map passkey sends the code that adds a passkey."`. In `run`, after the console check and before `logout`:

```go
	if len(inv.Args) == 1 && strings.EqualFold(inv.Args[0], "passkey") {
		code, fingerprint, err := m.claimer.PasskeyCode(ctx, inv.ActorXUID)
		switch {
		case errors.Is(err, mapclient.ErrNoPendingPasskey):
			return "There is no passkey waiting to be added for you. Start from the map page.", nil
		case err != nil:
			return "", fmt.Errorf("map: !map passkey: %w", err)
		}
		return fmt.Sprintf("Your map passkey code is %s, for the passkey with fingerprint %s. Type it into the map page that asked for it, within 2 minutes, only if that page shows the same fingerprint. It lets that browser log in to the map as you, as admin, without the game. Never give it to anyone, and never type it into a page someone else sent you. If you did not just add a passkey, type !map logout.", code, fingerprint), nil
	}
```

Change the logout reply to `"Every browser that was logged in to the map as you has been logged out, and any passkeys for your map login have been removed."`.

- [ ] **Step 4: Run the tests and confirm they pass**

Run: `cd minecraft/agent && go test -count=1 -race ./internal/mapclient/ ./internal/plugins/ ./cmd/agent/ </dev/null`
Expected: PASS. `cmd/agent` compiles because `*mapclient.Client` now has `PasskeyCode`.

- [ ] **Step 5: Commit**

```bash
gofmt -l minecraft/agent && go vet ./minecraft/agent/...
/usr/bin/git add minecraft/agent/internal/mapclient/client.go minecraft/agent/internal/mapclient/client_test.go minecraft/agent/internal/plugins/maplogin.go minecraft/agent/internal/plugins/maplogin_test.go
/usr/bin/git commit -m "feat(agent): whisper a map passkey code to the player who asks for it" -m "Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>
Assisted-by: Claude Code:claude-opus-5-5"
```

### Task 13: A failed whisper of a redacted reply logs no text

**Files:**
- Modify: `minecraft/agent/cmd/agent/main.go` (`speak` and its five callers in `handleCommand`), `minecraft/agent/internal/adapters/bridgeclient.go` (an exported classifier)
- Test: `minecraft/agent/cmd/agent/reply_logging_test.go`, `minecraft/agent/internal/adapters/bridgeclient_test.go`

**Interfaces:**
- Produces `func adapters.ErrorClass(err error) string`, which returns `"refused_<status>"` for a 4xx from the bridge, `"failed_<status>"` for any other status, or `"transport"`. It also produces `speak(ctx, log, pctx, actorXUID, command, reply string, redact bool)`.

- [ ] **Step 1: Write the failing tests**

Append to `cmd/agent/reply_logging_test.go`:

```go
// failingVoice is a bridge that refuses or fails every whisper, quoting it
// back the way the bridge's error body does.
type failingVoice struct{ recordingVoice }

func (v *failingVoice) Tell(_ context.Context, _ string, message string) error {
	return errors.New("bridge: POST /command: status 403: command not allowed: tellraw " + message)
}

// The whisper of a passkey code that failed to send must not put the code
// in the log by way of the error.
func TestRedactedReplyNeverReachesTheLogThroughAFailedWhisper(t *testing.T) {
	registry, pctx, _, eventBus, _, playerRoster, permResolver := newHarness(t)
	pctx.Voice = &failingVoice{}
	if err := registry.Register(replyPlugin{}); err != nil {
		t.Fatalf("register: %v", err)
	}
	out := captureAgentStdout(t, func() {
		log := logging.New("debug")
		handlePacket(context.Background(), chatPacket(playerXUID, "Steve", "!whereami"), selfXUID, nil, log, registry, pctx, eventBus, unlimitedRateLimit(), playerRoster, permResolver, testAnswering(), store.Nop{}, audit.Nop{}, newJoinTimes())
	})
	if strings.Contains(out, "123 64 -456") {
		t.Errorf("a failed whisper put the redacted reply in the log:\n%s", out)
	}
	if !strings.Contains(out, "voice_tell_failed") {
		t.Errorf("the failure itself was not logged:\n%s", out)
	}
}
```

Add `"errors"` to that file's imports.

Append to `internal/adapters/bridgeclient_test.go`:

```go
func TestErrorClass_SaysTheKindAndNotTheWords(t *testing.T) {
	cases := map[string]error{
		"refused_403": statusError{status: 403, err: errors.New("status 403: command not allowed: tellraw secret")},
		"failed_502":  statusError{status: 502, err: errors.New("status 502: secret")},
		"transport":   errors.New("dial tcp: secret"),
	}
	for want, err := range cases {
		if got := ErrorClass(err); got != want || strings.Contains(got, "secret") {
			t.Errorf("ErrorClass(%v) = %q, want %q", err, got, want)
		}
	}
}
```

- [ ] **Step 2: Run the tests and confirm they fail**

Run: `cd minecraft/agent && go test -count=1 -run 'TestRedactedReplyNeverReachesTheLogThroughAFailedWhisper|TestErrorClass' ./cmd/agent/ ./internal/adapters/ </dev/null`
Expected: FAIL. The first because the log line holds the error text; the second because `ErrorClass` is undefined.

- [ ] **Step 3: Implement**

In `internal/adapters/bridgeclient.go`:

```go
// ErrorClass names what kind of failure err was without any of its words,
// for a log line about a message whose words are not to be kept.
func ErrorClass(err error) string {
	var se statusError
	if errors.As(err, &se) {
		if se.status >= 400 && se.status < 500 {
			return "refused_" + strconv.Itoa(se.status)
		}
		return "failed_" + strconv.Itoa(se.status)
	}
	return "transport"
}
```

Add `"strconv"` to its imports if it is not there. In `cmd/agent/main.go`, give `speak` a final `redact bool` parameter. In its `Tell` branch:

```go
	if err := pctx.Voice.Tell(replyCtx, actorXUID, reply); err != nil {
		// The bridge's answer quotes the command back, which for a
		// redacted reply is the very text the log must not keep.
		detail := err.Error()
		if redact {
			detail = adapters.ErrorClass(err)
		}
		log.Error("voice_tell_failed", logging.Fields{"command": command, "actor": actorXUID, "error": detail})
	}
```

Pass `false` from the four callers that send fixed text (unknown, denied, timed out, failed). Pass the command's own `RedactReply` from the last one:

```go
	redact := false
	if cmd, ok := registry.Lookup(trigger.Command); ok && cmd.RedactReply {
		redact = true
		replied["reply_chars"] = len(reply)
	} else {
		replied["reply"] = reply
	}
```

Keep the existing logic that fills `replied`, rewritten as above, and then call `speak(settle, log, pctx, actorXUID, trigger.Command, reply, redact)`.

- [ ] **Step 4: Run the whole component's tests and confirm they pass**

Run: `cd minecraft/agent && go test -count=1 -race ./... </dev/null`
Expected: PASS.

- [ ] **Step 5: Commit, push, open the group's pull request**

```bash
gofmt -l minecraft/agent && go vet ./minecraft/agent/...
/usr/bin/git add minecraft/agent/cmd/agent/main.go minecraft/agent/cmd/agent/reply_logging_test.go minecraft/agent/internal/adapters/bridgeclient.go minecraft/agent/internal/adapters/bridgeclient_test.go
/usr/bin/git commit -m "fix(agent): keep a redacted reply's words out of the log when its whisper fails" -m "Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>
Assisted-by: Claude Code:claude-opus-5-5"
```

Open the PR as for Group 1. The point that needs attention is the whisper's exact text and `RedactReply` on `!map`. Merging releases a new `minecraft-server-agent` image, and production changes only when Group 3 runs it.

---

## Group 3: the chart (`jdw-deployments`, `feat/map-admin-access`)

This group needs the image versions Groups 1 and 2 released. Read them, don't guess them:

```bash
gh release list --repo jdwillmsen/gameops --limit 30 | grep -E '^(mcmap|agent)-v' | head -4
```

The newest `mcmap-vX.Y.Z` is the map's tag without the prefix, so `X.Y.Z`. The newest `agent-vA.B.C` is the agent's.

Create the worktree from the `jdw-deployments` checkout:

```bash
/usr/bin/git -C ~/projects/jdwillmsen/jdw-deployments fetch origin
/usr/bin/git -C ~/projects/jdwillmsen/jdw-deployments worktree add ~/worktrees/jdwillmsen/jdw-deployments/feat/map-admin-access -b feat/map-admin-access origin/main
```

Before opening the PR, check the base branch's gates as the global rules say. At the last check, `main` needed 0 approvals.

### Task 14: The switches, the origin, and the render guard

**Files:**
- Modify: `charts/minecraft-fwb/values.yaml` (`map.image.tag` and its history comment, `agent.image.tag`, a new `map.admin` block after `map.checks`), `charts/minecraft-fwb/templates/map.yaml` (env)
- Create: `tools/tests/test-map-admin.sh`

**Interfaces:**
- Produces the values `map.admin.enabled` (`true` in this PR, which is spec Rollout step 2), `map.admin.passkeys` (`false`) and `map.admin.loki.enabled` (`true`). It produces the env `ADMIN_ENABLED`, `PASSKEYS_ENABLED` and, only with passkeys, `PUBLIC_ORIGIN=https://<map.host>`.

- [ ] **Step 1: Write the failing test**

`tools/tests/test-map-admin.sh`:

```bash
#!/usr/bin/env bash
# Pins the map's admin access: the two switches and the order they may be
# turned on in, the one origin passkeys are bound to, and the alerts that
# tell the owner a passkey was added. Read from the rendered chart with the
# value files production applies.
set -euo pipefail

here="$(cd "$(dirname "$0")/../.." && pwd)"
chart="$here/charts/minecraft-fwb"
work="$(mktemp -d)"
trap 'rm -rf "$work"' EXIT

fail() { echo "FAIL: $1"; exit 1; }

render() {
  helm template minecraft-fwb "$chart" --namespace jdwillmsen-prd \
    -f "$chart/values.yaml" -f "$chart/values-prd.yaml" \
    -f "$chart/values-console-bridge.yaml" -f "$chart/values-map-pack.yaml" "$@"
}

env_of() {
  python3 - "$1" <<'PY'
import sys, yaml
for d in yaml.safe_load_all(open(sys.argv[1])):
    if d and d["kind"] == "Deployment" and d["metadata"]["name"].endswith("-map"):
        for e in d["spec"]["template"]["spec"]["containers"][0]["env"]:
            print(f'{e["name"]}={e.get("value", "<from secret>")}')
PY
}

render > "$work/prod.yaml" || fail "the chart does not render with production values"
env_of "$work/prod.yaml" > "$work/prod.env"
grep -qx 'ADMIN_ENABLED=true' "$work/prod.env" || fail "production does not set ADMIN_ENABLED=true"
grep -qx 'PASSKEYS_ENABLED=false' "$work/prod.env" || fail "production does not set PASSKEYS_ENABLED=false"
! grep -q '^PUBLIC_ORIGIN=' "$work/prod.env" || fail "PUBLIC_ORIGIN is set with passkeys off"

render --set map.admin.passkeys=true > "$work/on.yaml" || fail "the chart does not render with passkeys on"
env_of "$work/on.yaml" > "$work/on.env"
host="$(python3 -c 'import sys,yaml; print(yaml.safe_load(open(sys.argv[1]))["map"]["host"])' "$chart/values.yaml")"
grep -qx "PUBLIC_ORIGIN=https://$host" "$work/on.env" || fail "PUBLIC_ORIGIN is not https:// plus map.host"

if render --set map.admin.enabled=false --set map.admin.passkeys=true > /dev/null 2>&1; then
  fail "passkeys on with admin off rendered; the map would refuse to start"
fi
render --set map.admin.enabled=false --set map.admin.passkeys=false > "$work/off.yaml" || fail "both off, which is how admin is turned off, does not render"
env_of "$work/off.yaml" | grep -qx 'ADMIN_ENABLED=false' || fail "with admin off, ADMIN_ENABLED is not false"

echo "ok: map admin switches"
```

Then `chmod +x tools/tests/test-map-admin.sh`.

- [ ] **Step 2: Run it and confirm it fails**

Run: `tools/tests/test-map-admin.sh`
Expected: `FAIL: production does not set ADMIN_ENABLED=true`.

- [ ] **Step 3: Implement**

In `values.yaml`, after the `map.checks` block and at the same indentation:

```yaml
  # The map's admins are the game's own operators, read through the console
  # bridge from permissions.json, which is not in git and never will be. An
  # admin sees the server's status in the page and may add a passkey, which
  # logs them in without the game. See "Admin access" in the gameops map
  # README.
  admin:
    # Recognise operators as admins. Off, no admin route is served.
    enabled: true
    # Passkey login. Needs enabled: the chart refuses to render it alone,
    # because the map would refuse to start. To turn admin off, set both
    # to false in one change. Turned on by its own change once
    # mcmap_admin_operators has read at least 1.
    passkeys: false
    loki:
      # The Loki copy of the passkey-added alert. It does not evaluate until
      # the platform's Loki ruler reads this tenant's org; the
      # PrometheusRule is the one that fires meanwhile.
      enabled: true
```

Set `map.image.tag` to the released `X.Y.Z`, adding to the history comment above it, in its voice:

```yaml
    # X.Y.Z lets the game's operators be the map's admins, shows them the
    # server's status, and can let them add a passkey and log in with it
    # without the game; see admin below, which is what switches each on.
    # It also serves the map's own status on its internal port, with no
    # name, place or path in it, for agents to read.
```

Set `agent.image.tag` to `A.B.C` with a one-line note in its own history comment: `A.B.C whispers a map passkey code to the player who types !map passkey.`

In `templates/map.yaml`, after the `HISTORY_ENABLED` block:

```yaml
            {{- if and .Values.map.admin.passkeys (not .Values.map.admin.enabled) }}
            {{- fail "map.admin.passkeys needs map.admin.enabled: only an admin may add a passkey, and the map refuses to start with one on and not the other. To turn admin off, set both to false." }}
            {{- end }}
            - name: ADMIN_ENABLED
              value: {{ .Values.map.admin.enabled | quote }}
            - name: PASSKEYS_ENABLED
              value: {{ .Values.map.admin.passkeys | quote }}
            {{- if .Values.map.admin.passkeys }}
            # A passkey is bound to this exact origin.
            - name: PUBLIC_ORIGIN
              value: "https://{{ .Values.map.host }}"
            {{- end }}
```

- [ ] **Step 4: Run it and the existing map suite, and confirm they pass**

Run: `tools/tests/test-map-admin.sh && tools/tests/test-map.sh && shellcheck -s bash tools/tests/test-map-admin.sh`
Expected: `ok: map admin switches`, the existing suite passes, and shellcheck is quiet.

- [ ] **Step 5: Commit**

```bash
/usr/bin/git add charts/minecraft-fwb/values.yaml charts/minecraft-fwb/templates/map.yaml tools/tests/test-map-admin.sh
/usr/bin/git commit -m "feat(minecraft-fwb): run the map and agent with admin access, passkeys off" -m "Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>
Assisted-by: Claude Code:claude-opus-5-5"
```

### Task 15: The two alerts, with promtool tests

**Files:**
- Create: `charts/minecraft-fwb/templates/map-admin-rules.yaml`
- Modify: `tools/tests/test-map-admin.sh` (append the promtool section)

**Interfaces:**
- Produces the PrometheusRule `<map name>-admin` with `JdwillmsenMinecraftMapPasskeyAdded` and `JdwillmsenMinecraftMapPasskeyLoginRefused`. Both carry `severity: warning` and `tenant: jdwillmsen`, so that they route straight to Discord.

The spec writes the comparison "aggregated without pod/instance". The rules below aggregate `by (namespace)`. That drops pod and instance too, and also gives both halves of the `or` one label set, so the alert fires once and not twice.

- [ ] **Step 1: Write the failing promtool tests**

Append to `tools/tests/test-map-admin.sh`, before the final `echo`:

```bash
if ! command -v promtool >/dev/null 2>&1; then
  echo "SKIP: promtool not on PATH; map admin alerts not verified"
  echo "ok: map admin switches"
  exit 0
fi

python3 - "$work/prod.yaml" > "$work/rules.yaml" <<'PY'
import sys, yaml
for doc in yaml.safe_load_all(open(sys.argv[1])):
    if doc and doc.get("kind") == "PrometheusRule" and doc["metadata"]["name"].endswith("-map-admin"):
        print(yaml.safe_dump({"groups": doc["spec"]["groups"]}, sort_keys=False))
PY
[ -s "$work/rules.yaml" ] || fail "could not extract the map admin rules from the chart"
promtool check rules "$work/rules.yaml" >/dev/null || fail "promtool rejected the map admin rules"

added_labels='
          - exp_labels:
              severity: warning
              tenant: jdwillmsen
              namespace: jdwillmsen-prd
            exp_annotations:
              summary: "A passkey was added to the FWB map"
              description: "A passkey for logging in to the map as an admin was confirmed in the last 15 minutes. If nobody you know added one, the player it belongs to should type !map logout in the game, which logs out every browser and removes every passkey they hold."'

cat > "$work/tests.yaml" <<TESTS
rule_files:
  - rules.yaml
evaluation_interval: 1m
tests:
  # The first passkey ever, from the gauge and counter declared at zero.
  - interval: 1m
    input_series:
      - series: 'mcmap_admin_passkeys{namespace="jdwillmsen-prd", pod="map-a", state="active"}'
        values: '0x60 1x60'
      - series: 'mcmap_admin_passkeys_registered_total{namespace="jdwillmsen-prd", pod="map-a", result="confirmed"}'
        values: '0x60 1x60'
    alert_rule_test:
      - eval_time: 65m
        alertname: JdwillmsenMinecraftMapPasskeyAdded
        exp_alerts:$added_labels
      - eval_time: 100m
        alertname: JdwillmsenMinecraftMapPasskeyAdded
        exp_alerts: []

  # A second one added.
  - interval: 1m
    input_series:
      - series: 'mcmap_admin_passkeys{namespace="jdwillmsen-prd", pod="map-a", state="active"}'
        values: '1x60 2x60'
      - series: 'mcmap_admin_passkeys_registered_total{namespace="jdwillmsen-prd", pod="map-a", result="confirmed"}'
        values: '1x60 2x60'
    alert_rule_test:
      - eval_time: 65m
        alertname: JdwillmsenMinecraftMapPasskeyAdded
        exp_alerts:$added_labels

  # Revoke all, then add one: the count ends lower than it was, and the
  # confirmation is what fires.
  - interval: 1m
    input_series:
      - series: 'mcmap_admin_passkeys{namespace="jdwillmsen-prd", pod="map-a", state="active"}'
        values: '3x60 0x5 1x55'
      - series: 'mcmap_admin_passkeys_registered_total{namespace="jdwillmsen-prd", pod="map-a", result="confirmed"}'
        values: '3x65 4x55'
    alert_rule_test:
      - eval_time: 70m
        alertname: JdwillmsenMinecraftMapPasskeyAdded
        exp_alerts:$added_labels

  # A Recreate restart with nothing confirmed: the old pod's series goes
  # stale at 61m, nothing is scraped at 62m or 63m, and a new pod reports the
  # same count from 64m, its counter back at zero. Silent throughout.
  - interval: 1m
    input_series:
      - series: 'mcmap_admin_passkeys{namespace="jdwillmsen-prd", pod="map-a", state="active"}'
        values: '2x60 stale'
      - series: 'mcmap_admin_passkeys{namespace="jdwillmsen-prd", pod="map-b", state="active"}'
        values: '_x64 2x60'
      - series: 'mcmap_admin_passkeys_registered_total{namespace="jdwillmsen-prd", pod="map-a", result="confirmed"}'
        values: '2x60 stale'
      - series: 'mcmap_admin_passkeys_registered_total{namespace="jdwillmsen-prd", pod="map-b", result="confirmed"}'
        values: '_x64 0x60'
    alert_rule_test:
      - { eval_time: 62m, alertname: JdwillmsenMinecraftMapPasskeyAdded, exp_alerts: [] }
      - { eval_time: 66m, alertname: JdwillmsenMinecraftMapPasskeyAdded, exp_alerts: [] }
      - { eval_time: 80m, alertname: JdwillmsenMinecraftMapPasskeyAdded, exp_alerts: [] }
      - { eval_time: 100m, alertname: JdwillmsenMinecraftMapPasskeyAdded, exp_alerts: [] }

  # The same restart followed by a confirmation.
  - interval: 1m
    input_series:
      - series: 'mcmap_admin_passkeys{namespace="jdwillmsen-prd", pod="map-a", state="active"}'
        values: '2x60 stale'
      - series: 'mcmap_admin_passkeys{namespace="jdwillmsen-prd", pod="map-b", state="active"}'
        values: '_x64 2x10 3x50'
      - series: 'mcmap_admin_passkeys_registered_total{namespace="jdwillmsen-prd", pod="map-a", result="confirmed"}'
        values: '2x60 stale'
      - series: 'mcmap_admin_passkeys_registered_total{namespace="jdwillmsen-prd", pod="map-b", result="confirmed"}'
        values: '_x64 0x10 1x50'
    alert_rule_test:
      - eval_time: 80m
        alertname: JdwillmsenMinecraftMapPasskeyAdded
        exp_alerts:$added_labels

  # Confirmed before the new pod's first scrape: its counter is first seen
  # at 1, which increase() cannot see, and the gauge must fire instead.
  - interval: 1m
    input_series:
      - series: 'mcmap_admin_passkeys{namespace="jdwillmsen-prd", pod="map-a", state="active"}'
        values: '2x60 stale'
      - series: 'mcmap_admin_passkeys{namespace="jdwillmsen-prd", pod="map-b", state="active"}'
        values: '_x64 3x60'
      - series: 'mcmap_admin_passkeys_registered_total{namespace="jdwillmsen-prd", pod="map-a", result="confirmed"}'
        values: '2x60 stale'
      - series: 'mcmap_admin_passkeys_registered_total{namespace="jdwillmsen-prd", pod="map-b", result="confirmed"}'
        values: '_x64 1x60'
    alert_rule_test:
      - eval_time: 70m
        alertname: JdwillmsenMinecraftMapPasskeyAdded
        exp_alerts:$added_labels

  # A removal.
  - interval: 1m
    input_series:
      - series: 'mcmap_admin_passkeys{namespace="jdwillmsen-prd", pod="map-a", state="active"}'
        values: '2x60 1x60'
      - series: 'mcmap_admin_passkeys_registered_total{namespace="jdwillmsen-prd", pod="map-a", result="confirmed"}'
        values: '2x120'
    alert_rule_test:
      - { eval_time: 65m, alertname: JdwillmsenMinecraftMapPasskeyAdded, exp_alerts: [] }

  # An outage longer than the window, then a restart with nothing
  # confirmed: the window is empty, and nothing fires.
  - interval: 1m
    input_series:
      - series: 'mcmap_admin_passkeys{namespace="jdwillmsen-prd", pod="map-a", state="active"}'
        values: '2x10 stale'
      - series: 'mcmap_admin_passkeys{namespace="jdwillmsen-prd", pod="map-b", state="active"}'
        values: '_x70 2x60'
      - series: 'mcmap_admin_passkeys_registered_total{namespace="jdwillmsen-prd", pod="map-a", result="confirmed"}'
        values: '1x10 stale'
      - series: 'mcmap_admin_passkeys_registered_total{namespace="jdwillmsen-prd", pod="map-b", result="confirmed"}'
        values: '_x70 0x60'
    alert_rule_test:
      - { eval_time: 75m, alertname: JdwillmsenMinecraftMapPasskeyAdded, exp_alerts: [] }
      - { eval_time: 100m, alertname: JdwillmsenMinecraftMapPasskeyAdded, exp_alerts: [] }

  # Refusals after the signature page; those before it do not.
  - interval: 1m
    input_series:
      - series: 'mcmap_admin_logins_total{namespace="jdwillmsen-prd", pod="map-a", result="counter"}'
        values: '0x30 1x30'
      - series: 'mcmap_admin_logins_total{namespace="jdwillmsen-prd", pod="map-a", result="bad_assertion"}'
        values: '0x30 9x30'
    alert_rule_test:
      - eval_time: 40m
        alertname: JdwillmsenMinecraftMapPasskeyLoginRefused
        exp_alerts:
          - exp_labels:
              severity: warning
              tenant: jdwillmsen
              namespace: jdwillmsen-prd
            exp_annotations:
              summary: "A real passkey was turned away at the FWB map"
              description: "In the last 15 minutes a passkey login whose signature was good was refused: a counter that went back (a copied key), a replay, a holder who is no longer an operator, or a passkey never confirmed. mcmap_admin_logins_total by result says which."
  - interval: 1m
    input_series:
      - series: 'mcmap_admin_logins_total{namespace="jdwillmsen-prd", pod="map-a", result="bad_assertion"}'
        values: '0x30 9x30'
      - series: 'mcmap_admin_logins_total{namespace="jdwillmsen-prd", pod="map-a", result="unknown_credential"}'
        values: '0x30 9x30'
    alert_rule_test:
      - { eval_time: 40m, alertname: JdwillmsenMinecraftMapPasskeyLoginRefused, exp_alerts: [] }
TESTS

if ! out="$(cd "$work" && promtool test rules tests.yaml 2>&1)"; then
  echo "$out"
  fail "the map admin alerts do not behave as tested"
fi
```

- [ ] **Step 2: Run it and confirm it fails**

Run: `tools/tests/test-map-admin.sh`
Expected: `FAIL: could not extract the map admin rules from the chart` (or `SKIP` where promtool is missing; install it as `ci.yml` does to see the failure).

- [ ] **Step 3: Implement**

`charts/minecraft-fwb/templates/map-admin-rules.yaml`:

```yaml
{{- if and .Values.map.enabled .Values.map.admin.enabled }}
# Labelled with the tenant so that they reach the owner straight away: a
# warning without that label goes only to the AI-SRE relay.
apiVersion: monitoring.coreos.com/v1
kind: PrometheusRule
metadata:
  name: {{ include "map.name" . }}-admin
  namespace: {{ .Release.Namespace }}
spec:
  groups:
    - name: minecraft-fwb-map-admin
      rules:
        - alert: JdwillmsenMinecraftMapPasskeyAdded
          # Two ways to see one event. The counter sees every confirmation,
          # including one that follows revoking every other passkey, which
          # leaves the count lower than it was. The gauge, read back from
          # the volume at start, sees a confirmation made before a new pod's
          # first scrape, which its counter never shows at zero. The window
          # is half an hour so that a restart's gap of a few scrapes does not
          # empty it. By namespace, so both halves have one label set and
          # fire as one alert.
          expr: |
            (
              sum by (namespace) (increase(mcmap_admin_passkeys_registered_total{namespace="{{ .Release.Namespace }}", result="confirmed"}[15m])) > 0
            )
            or
            (
              max by (namespace) (mcmap_admin_passkeys{namespace="{{ .Release.Namespace }}", state="active"})
                > max by (namespace) (max_over_time(mcmap_admin_passkeys{namespace="{{ .Release.Namespace }}", state="active"}[30m] offset 15m))
            )
          labels:
            severity: warning
            tenant: jdwillmsen
            namespace: {{ .Release.Namespace }}
          annotations:
            summary: "A passkey was added to the FWB map"
            description: "A passkey for logging in to the map as an admin was confirmed in the last 15 minutes. If nobody you know added one, the player it belongs to should type !map logout in the game, which logs out every browser and removes every passkey they hold."
        - alert: JdwillmsenMinecraftMapPasskeyLoginRefused
          # Only refusals made after the signature verified: someone held a
          # real passkey and was still turned away. Those before it are
          # anyone on the internet's to make, and stay on the dashboard.
          expr: |
            sum by (namespace) (increase(mcmap_admin_logins_total{namespace="{{ .Release.Namespace }}", result=~"counter|replayed|not_operator|pending"}[15m])) > 0
          labels:
            severity: warning
            tenant: jdwillmsen
            namespace: {{ .Release.Namespace }}
          annotations:
            summary: "A real passkey was turned away at the FWB map"
            description: "In the last 15 minutes a passkey login whose signature was good was refused: a counter that went back (a copied key), a replay, a holder who is no longer an operator, or a passkey never confirmed. mcmap_admin_logins_total by result says which."
{{- end }}
```

- [ ] **Step 4: Run it and confirm it passes**

Run: `tools/tests/test-map-admin.sh`
Expected: `ok: map admin switches`, with promtool's tests passing. If promtool reports a case's alert at a different `eval_time` than expected, recheck the series against the window arithmetic in each case's comment. Do not loosen an expectation to make it pass.

- [ ] **Step 5: Commit**

```bash
/usr/bin/git add charts/minecraft-fwb/templates/map-admin-rules.yaml tools/tests/test-map-admin.sh
/usr/bin/git commit -m "feat(minecraft-fwb): tell the owner when a map passkey is added or a real one refused" -m "Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>
Assisted-by: Claude Code:claude-opus-5-5"
```

### Task 16: The Loki rule, and the chart README

**Files:**
- Create: `charts/minecraft-fwb/templates/map-admin-loki-rule.yaml`
- Modify: `tools/tests/test-map-admin.sh` (a render check), `charts/minecraft-fwb/README.md`

- [ ] **Step 1: Write the failing check**

In `test-map-admin.sh`, after the switch checks and before the promtool section:

```bash
python3 - "$work/prod.yaml" <<'PY' || fail "the Loki copy of the passkey alert is missing or wrong"
import sys, yaml
docs = [d for d in yaml.safe_load_all(open(sys.argv[1])) if d]
cms = [d for d in docs if d["kind"] == "ConfigMap" and d["metadata"].get("labels", {}).get("loki_rule") == "true" and "map-admin" in d["metadata"]["name"]]
assert len(cms) == 1, f"{len(cms)} map admin Loki ConfigMaps"
text = "".join(cms[0]["data"].values())
for want in ("map passkey registered", "tenant: jdwillmsen", "JdwillmsenMinecraftMapPasskeyAddedLogged", 'container="map"'):
    assert want in text, want
PY
```

- [ ] **Step 2: Run it and confirm it fails**

Run: `tools/tests/test-map-admin.sh`
Expected: `FAIL: the Loki copy of the passkey alert is missing or wrong`.

- [ ] **Step 3: Implement**

`charts/minecraft-fwb/templates/map-admin-loki-rule.yaml`:

```yaml
{{- if and .Values.map.enabled .Values.map.admin.enabled .Values.map.admin.loki.enabled }}
# The log's copy of the passkey-added alert. It does not evaluate yet, for
# the reason world-integrity-loki-rule.yaml gives: the platform's Loki rules
# sidecar is mounted for the platform org, and these logs are in this
# tenant's. It ships so that it starts working the day that is fixed;
# until then JdwillmsenMinecraftMapPasskeyAdded in map-admin-rules.yaml is
# the alert that fires.
apiVersion: v1
kind: ConfigMap
metadata:
  name: {{ include "map.name" . }}-admin-loki-rules
  namespace: {{ .Release.Namespace }}
  labels:
    loki_rule: "true"
data:
  minecraft-fwb-map-admin.yaml: |
    groups:
      - name: minecraft-fwb-map-admin-logs
        interval: 1m
        rules:
          - alert: JdwillmsenMinecraftMapPasskeyAddedLogged
            # Selected by container, not pod: this cluster's log pipeline
            # ships no pod stream label. The map's lines are JSON, and the
            # message is matched as a field, so text a player chose cannot
            # raise it.
            expr: |
              sum(count_over_time({namespace="{{ .Release.Namespace }}", container="map"} |= "map passkey registered" | json | msg="map passkey registered" [15m])) > 0
            labels:
              severity: warning
              tenant: jdwillmsen
              namespace: {{ .Release.Namespace }}
            annotations:
              summary: "A passkey was added to the FWB map (from the log)"
              description: "The map logged a confirmed passkey in the last 15 minutes. If nobody you know added one, the player it belongs to should type !map logout in the game."
{{- end }}
```

In `charts/minecraft-fwb/README.md`, add a section `## Map admin access`. In the README's voice it says:
- what the two values do;
- the order they are turned on in (admin, wait for `mcmap_admin_operators` at least 1, then passkeys);
- how to turn them off (passkeys alone, or both in one change; Argo sync plus a restart);
- the two alerts and where they route;
- that the Loki copy is inert until the platform's ruler reads tenant orgs;
- that `tools/tests/test-map-admin.sh` pins all of it.

- [ ] **Step 4: Run every chart suite and confirm they pass**

Run: `for s in tools/tests/test-*.sh; do "$s" || exit 1; done && shellcheck -s bash tools/tests/*.sh && helm lint charts/minecraft-fwb -f charts/minecraft-fwb/values.yaml -f charts/minecraft-fwb/values-prd.yaml`
Expected: every suite prints its `ok` line, and lint passes.

- [ ] **Step 5: Commit, push, and open the PR**

```bash
/usr/bin/git add charts/minecraft-fwb/templates/map-admin-loki-rule.yaml tools/tests/test-map-admin.sh charts/minecraft-fwb/README.md
/usr/bin/git commit -m "feat(minecraft-fwb): ship the log copy of the passkey alert, and document admin access" -m "Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>
Assisted-by: Claude Code:claude-opus-5-5"
```

The map restarts when this merges. Follow the map-restart rule: roll when the server is empty, or announce it with `tools/mc announce` first. The game server is not restarted. The points that need attention in the PR are the alert expressions and their promtool cases.

### Task 17: Rollout: turn passkeys on (spec Rollout steps 3 and 4)

This is a one-value pull request in `jdw-deployments`, on branch `feat/map-admin-passkeys` from `origin/main`, after Task 16 has merged and synced.

- [ ] **Step 1: Check the operator gauge reads at least 1**

```bash
kubectl get --raw '/api/v1/namespaces/monitoring/services/platform-kube-prometheus-s-prometheus:9090/proxy/api/v1/query?query=mcmap_admin_operators%7Bnamespace%3D%22jdwillmsen-prd%22%7D' | jq '.data.result[].value[1]'
```

Expected: `"1"` or more. If it reads `"0"` or nothing, stop. The operator list is empty or unreadable, and passkeys must wait. At least 1 is necessary, not sufficient: it does not say the owner is on the list.

- [ ] **Step 2: Check the internal status agrees**

```bash
kubectl get --raw "/api/v1/namespaces/jdwillmsen-prd/services/http:jdwillmsen-minecraft-fwb-prd-map:internal/proxy/internal/v1/status" | jq '{operators, features: {admin: .features.admin, passkeys: .features.passkeys}}'
```

Expected: `"operators": "live"`, `admin: true`, `passkeys: false`. The Service is `<release>-map`, and the release in production is `jdwillmsen-minecraft-fwb-prd` (seen 2026-10-14).

- [ ] **Step 3: Flip the value, run the suite, commit**

Set `map.admin.passkeys: true` in `charts/minecraft-fwb/values.yaml`. Then:

```bash
tools/tests/test-map-admin.sh && tools/tests/test-map.sh
/usr/bin/git add charts/minecraft-fwb/values.yaml
/usr/bin/git commit -m "feat(minecraft-fwb): let map admins add passkeys" -m "Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>
Assisted-by: Claude Code:claude-opus-5-5"
```

Open the PR, merge it once green under the map-restart rule, and after sync check that `/internal/v1/status` says `passkeys: true`.

### Task 18: Hand-off: the check only the owner can make

No agent can log in as an operator, so phase 1 is shipped but not proven end to end until the owner does this. Put it, word for word, in the final report to the user:

1. Log in to the map through the game. The page shows Account, and inside it an Admin section with the server's status.
2. Under Admin, name a passkey and press Add a passkey. Your device asks to make one, and the page shows a fingerprint.
3. Type `!map passkey` in the game. Check that the whisper shows the same fingerprint, then type its code into the page.
4. On another device or a private window, press Log in with a passkey on the login screen.
5. Expect the alert "A passkey was added to the FWB map" in Discord within about 15 minutes.

---

## Group 4: the bridge's logging (`gameops`, `fix/bridge-tellraw-logging`)

Nothing in phase 1 depends on this group. It closes the last place a failed whisper's code could be logged.

### Task 19: The bridge logs and answers a `tellraw` by its rule name only

**Files:**
- Modify: `minecraft/bridge/http.go` (`handleCommand`)
- Test: `minecraft/bridge/http_test.go`

- [ ] **Step 1: Write the failing test**

Append to `minecraft/bridge/http_test.go`:

```go
// A tellraw carries a whisper, which can be a passkey code meant for one
// player. Refused or failed, the bridge says which rule and nothing more.
func TestCommand_ATellrawIsNeverQuotedBack(t *testing.T) {
	var log bytes.Buffer
	mux := newMux(&server{
		cfg:     Config{BridgeToken: "tok", DataDir: t.TempDir()},
		console: testConsole(), // never connected, so an allowed command fails to send
		logger:  slog.New(slog.NewTextHandler(&log, nil)),
	})
	secret := "ABC234"
	for name, cmd := range map[string]string{
		"refused": `tellraw Steve {"no":"` + secret + `"}`,
		"failed":  `tellraw Steve {"rawtext":[{"text":"` + secret + `"}]}`,
	} {
		rec := postCommand(t, mux, cmd)
		if rec.Code == http.StatusOK {
			t.Fatalf("%s: answered 200", name)
		}
		if strings.Contains(rec.Body.String(), secret) {
			t.Errorf("%s: the answer quotes the whisper: %s", name, rec.Body)
		}
	}
	if strings.Contains(log.String(), secret) {
		t.Errorf("the log quotes a whisper:\n%s", log.String())
	}
	if !strings.Contains(log.String(), "tellraw") {
		t.Error("the log does not say which rule, so this test saw nothing")
	}
	// Other commands are logged as they are today.
	log.Reset()
	_ = postCommand(t, mux, "op Steve")
	if !strings.Contains(log.String(), "op Steve") {
		t.Errorf("a refused command that is not a tellraw is no longer logged as it was:\n%s", log.String())
	}
}
```

`postCommand` is the existing helper this file's kick tests use. Add `"bytes"` and `"strings"` to the imports if they are not there.

- [ ] **Step 2: Run it and confirm it fails**

Run: `cd minecraft/bridge && go test -count=1 -run TestCommand_ATellraw ./... </dev/null`
Expected: FAIL, with the log and the 403 body quoting the secret.

- [ ] **Step 3: Implement**

In `http.go`:

```go
// spoken is a command as it may be written down or answered with. A
// tellraw is a whisper to one player, and its words are theirs, so it is
// named by its rule only.
func spoken(cmd string) string {
	if strings.HasPrefix(cmd, "tellraw ") {
		return "tellraw"
	}
	return cmd
}
```

In `handleCommand`, in the refusal branch:

```go
		s.logger.Info("command refused", "command", spoken(req.Command), "error", spokenErr(req.Command, err))
		http.Error(w, spokenErr(req.Command, err), http.StatusForbidden)
```

In the send-failure branch:

```go
		s.logger.Warn("command send failed", "command", spoken(req.Command), "error", spokenErr(req.Command, err))
		http.Error(w, spokenErr(req.Command, err), http.StatusBadGateway)
```

with:

```go
func spokenErr(cmd string, err error) string {
	if strings.HasPrefix(cmd, "tellraw ") {
		if errors.Is(err, ErrCommandNotAllowed) {
			return "command not allowed: tellraw"
		}
		return "tellraw could not be sent"
	}
	return err.Error()
}
```

- [ ] **Step 4: Run the component's tests and confirm they pass**

Run: `cd minecraft/bridge && go test -count=1 -race ./... </dev/null`
Expected: PASS.

- [ ] **Step 5: Commit, push, open the PR**

```bash
gofmt -l minecraft/bridge && go vet ./minecraft/bridge/...
/usr/bin/git add minecraft/bridge/http.go minecraft/bridge/http_test.go
/usr/bin/git commit -m "fix(bridge): name a refused or failed tellraw by its rule, never its words" -m "Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>
Assisted-by: Claude Code:claude-opus-5-5"
```

Merging releases a new `mc-console-bridge` image. Nothing runs it until Task 20.

### Task 20: Rollout: run the new bridge (spec Rollout step 5)

This is a `jdw-deployments` PR on branch `chore/console-bridge-tellraw-logging`. **It restarts the game server.**

- [ ] **Step 1: Read the released version**

```bash
gh release list --repo jdwillmsen/gameops --limit 40 | grep -E '^bridge-v' | head -1
```

- [ ] **Step 2: Change the literal and run the suites**

In `charts/minecraft-fwb/values.yaml`, change the `console-bridge` sidecar's `image: "ghcr.io/jdwillmsen/mc-console-bridge:…"` (a literal, near line 379) to the new version. Add one line to the history comment above it: `<version> names a refused or failed tellraw by its rule and never quotes the whisper.` Then:

```bash
for s in tools/tests/test-*.sh; do "$s" || exit 1; done
```

- [ ] **Step 3: Commit, open the PR, and merge only under the game-server restart rule**

```bash
/usr/bin/git add charts/minecraft-fwb/values.yaml
/usr/bin/git commit -m "chore(minecraft-fwb): run the bridge that never quotes a whisper" -m "Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>
Assisted-by: Claude Code:claude-opus-5-5"
```

This restarts the game server, so it goes through the restart-and-announce path. Merge it only when the server is empty, or with the owner's explicit go-ahead. It can wait for the next restart that is needed anyway. Until it runs, a code from a whisper that failed to send can sit in the bridge's log. Such a code was never delivered, is single use, and expires in 2 minutes.

---

## Self-review

Run against the spec after writing; fixes are already applied above.

**Spec coverage.**

| Spec requirement | Task |
|---|---|
| D1: the operator list, 10 s cache, last-known-good with `confirmedAt`, a 404 falls back, startup and hourly lookups, the gauge | 2 |
| D1: duplicates follow the bridge's last-wins rule | 2 (the bridge's map; nothing in the map changes) |
| D2: `gateAdmin`, `/api/me`, the switches' behaviour, no error with login off or bridge down | 1, 6 |
| D3: options (RP ID, residentKey, UV, attestation none, ES256 and RS256, name and displayName, random handle, excludeCredentials) | 5 |
| D3: one pending per XUID, refused not replaced, cancel by the same session | 4, 7 |
| D3: the fingerprint in page and whisper, and the credential ID in confirm | 4, 7, 10, 12 |
| D3: the code from game to page, three tries, 2 minutes | 4, 7, 12 |
| D3: §7.1 and §7.2 steps, stateless login challenge with its own HMAC label, atomic used set, counter rules | 5 |
| D3: passkey session 12 h, `m`/`k`, ended by removal or switch | 3, 8 |
| D3: the orphaned credential, `signalUnknownCredential` or a note | 10 |
| D3: the bridge's three log paths for a code | 13 (agent), 19 (bridge) |
| D5: admin status; player-gated list, remove, logout-all | 8, 9, 10 |
| D6: every log line and metric, declared at zero, no gamertag or code in new lines | 6, 7, 8 |
| D6: both alerts with tenant labels and their promtool cases; the Loki copy | 15, 16 |
| D7: revocation deletes passkeys; the new `!map logout` text; both switches off together | 8, 12, 14 |
| D8: the internal status, exact fields, the admin copy's two extra fields | 9 |
| Rollout steps 1 to 5 and the owner's check | Groups 1 to 4, Tasks 17, 18, 20 |
| Tests listed in the spec, page tests and e2e | each task; 10; 11 |

**Gaps found and how each was settled:**

- The spec's "aggregated without pod/instance" became `by (namespace)`. The reason is given in Task 15.
- The spec's `disabled` login result has no natural path. It is counted when a passkey login would issue a cookie that sessions cannot check (Task 7), so that a wiring mistake shows on the dashboard.
- Nothing else in the spec lacks a task.

**Placeholder scan.** The version numbers in Group 3 and Task 20 are read from `gh release list` in a step, because they do not exist until Groups 1, 2 and 4 are released. The Service name in Task 17 is the one production has.

**Type consistency.** These names are used the same way everywhere:
- `KeyOf`, `Fingerprint`, `LogName`
- `Passkeys.{StartPending, HasPending, Cancel, IssueCode, Confirm, Lookup, Use, List, Remove, RemoveAll, RefreshGamertag, Counts, Counted, Expired}`
- `Ceremonies.{BeginRegistration, FinishRegistration, BeginLogin, FinishLogin}`
- `auth.Result*`, `auth.MethodPasskey`, `Sessions.{Passkeys, PasskeyTTL, IssuePasskey}`, `SessionFromContext`
- `operators.{Open, NewBridge, Live, LastKnownGood, None}`
- `server.{CountPasskeys, CountPasskeyExpired, declareAdminMetrics}`
- `mapclient.{PasskeyCode, ErrNoPendingPasskey}`, `adapters.ErrorClass`

`FinishLogin` returns the passkey it found together with a refusal after the signature, and Task 7 relies on that.

**Review Focus.** The five lines at the top each have their test in the owning task: Tasks 5 and 10 (labels), 4 and 10 (restart mid-registration), 4 (double confirm), 2 (operator spelling) and 7 (garbage bodies).
