# Crew Wave 1 — Lab World and Empty Worker Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** A lab Bedrock server and a new `minecraft/crew` worker, both running on the cluster through ArgoCD. The worker joins the lab, tracks its own position, health and life state from packets, serves that state over an HTTP status API, survives deaths and server restarts, and does nothing else.

**Architecture:** `crew` follows afkbot's shape: one process, a reconnect loop, and one session per connection. The connection plumbing comes from `minecraft/agent/pkg/*`. Two new packages carry the worker-specific parts. `internal/belief` is a thread-safe tracker that turns packets into a `Snapshot`. `internal/statusapi` serves that snapshot. Wave 1 has no presence client: crew runs only on the lab, and parking crew is `crew-control`'s job from Wave 3 (design D5).

**Tech Stack:** Go 1.27 (single module `github.com/jdwillmsen/gameops`), `github.com/sandertv/gophertunnel` v1.62.0, `github.com/go-gl/mathgl/mgl32`, the standard library's `net/http`, Docker Compose, `itzg/minecraft-bedrock-server:2026.9.2`.

**Spec:** `docs/specs/2026-10-14-crew-design.md`

## Global Constraints

- Do not change any file under `minecraft/afkbot/`, `minecraft/agent/`, `minecraft/mcmap/`, `minecraft/bridge/` or `internal/presenceapi/`. Crew imports `minecraft/agent/pkg/*` read-only.
- No live LLM, model client or chat handling anywhere in `minecraft/crew`.
- Crew never connects to FWB. Its only target is the lab server. In `jdw-deployments`, Wave 1 adds only the new `charts/crew-lab` chart and its ArgoCD entry (Task 6). It does not touch `charts/minecraft-fwb`.
- Never log a position, coordinate or seed. Positions appear only in `/v1/state` response bodies. No FWB coordinates, seeds or player names in code, tests, fixtures, docs, commits or PR text.
- `STATUS_ADDR` defaults to loopback (`127.0.0.1:8080`). Only the compose file binds it wider, and only inside the compose network.
- The lab server image is pinned to the tag FWB uses (`2026.9.2`) with `VERSION=LATEST`, the same as FWB, so both speak the same protocol.
- Cheats are allowed on the lab and on nothing else.
- Comments explain *why*, never *what*. Match afkbot's density and tone. No URLs, ticket IDs or names in comments.
- Every commit carries `Co-Authored-By:` and `Assisted-by: <agent>:<model-id>` trailers. The branch is `feat/JDW-<n>-crew-wave1` (ticket key from the JDW epic). Merges are rebase-only.

## Review Focus

1. **Another entity's movement packet**: belief must ignore a `MovePlayer` or `UpdateAttributes` whose runtime ID is not crew's own. A player walking past must not teleport crew's reported position. (Task 2: `TestOtherEntitiesDoNotMoveTheUnit`)
2. **Crew dies and sits on the death screen**: `/readyz` must return 503 while dead and 200 again after respawn. "Connected" alone is not ready. (Task 3: `TestReadyzIsUnavailableWhileDead`)
3. **Lab server restarts mid-session**: the loop must reconnect with backoff, and `/readyz` must flip to 503 between sessions rather than reporting the stale spawned state. (Task 2: `TestResetForgetsTheSession`; Task 4: `TestLoopResetsBeliefWhenASessionEnds`)
4. **Positions leaking into logs**: no log line may carry a coordinate. (Task 4: `TestSessionLogFieldsCarryNoPosition`; Task 5: a grep of the compose logs)
5. **Status API exposed on every interface by default**: an unset `STATUS_ADDR` must bind loopback only. (Task 1: `TestStatusAddrDefaultsToLoopback`)

## Dispatch map

```
Task 1 (lab + scaffold + config) ──► Task 2 (belief) ──┐
                                 └─► Task 3 (statusapi)┴─► Task 4 (cmd/crew) ──► Task 5 (image, compose, docs, live check) ──► Task 6 (cluster: crew-lab chart + ArgoCD)
```

Tasks 2 and 3 may run in parallel worktrees once Task 1 has merged. Task 3 depends only on `belief.Snapshot`'s shape, which is fixed below. Implementers use Sonnet. Reviewers use Opus.

**Owner prerequisite before Task 5:** a Microsoft account for `crew-1` that owns Bedrock (any edition purchase that includes the Bedrock licence). Its gamertag goes in `minecraft/crew/lab/.env`, which is git-ignored.

---

### Task 1: Lab world, component scaffold, and config

**Files:**
- Create: `minecraft/crew/lab/compose.yaml`
- Create: `minecraft/crew/lab/.env.example`
- Create: `minecraft/crew/component.yaml`
- Create: `minecraft/crew/internal/config/config.go`
- Test: `minecraft/crew/internal/config/config_test.go`
- Modify: `.gitignore` (append one line)

**Interfaces:**
- Produces: `config.Load() (config.Config, error)` with fields `Host string`, `Port int`, `Username string`, `ViewDistance uint8`, `AuthCacheDir string`, `ReconnectMinMs int`, `ReconnectMaxMs int`, `StatusAddr string`.

- [ ] **Step 1: Write the lab compose file (server only; Task 5 adds crew)**

`minecraft/crew/lab/compose.yaml`:

```yaml
# The crew lab: a throwaway Bedrock world where crew and cameras may use
# cheats. Never point anything here at FWB, and never point crew at FWB.
name: crew-lab

services:
  lab:
    # Same tag and VERSION policy as FWB, so a protocol bump that breaks crew
    # here is the one that would break it there.
    image: itzg/minecraft-bedrock-server:2026.9.2
    environment:
      EULA: "TRUE"
      VERSION: "LATEST"
      SERVER_NAME: "crew-lab"
      LEVEL_NAME: "crew-lab"
      GAMEMODE: "survival"
      DIFFICULTY: "normal"
      ALLOW_CHEATS: "true"
      ONLINE_MODE: "true"
      # Ops are granted from the console with `send-command op <gamertag>`,
      # so no gamertag needs to be committed here.
    ports:
      - "19132:19132/udp"
    volumes:
      - lab-world:/data
    stdin_open: true
    tty: true

volumes:
  lab-world:
```

`minecraft/crew/lab/.env.example`:

```
# Copy to .env (git-ignored). The Microsoft account crew-1 signs in as.
CREW_USERNAME=
```

Append to `.gitignore`:

```
minecraft/crew/lab/.env
```

- [ ] **Step 2: Bring the lab up and confirm it answers**

Run: `docker compose -f minecraft/crew/lab/compose.yaml up -d lab && sleep 40 && docker compose -f minecraft/crew/lab/compose.yaml logs lab | grep -i "server started"`
Expected: one line containing `Server started`. If the image prints a different readiness line, record the real line in the PR description and use it.

- [ ] **Step 3: Write the component manifest**

`minecraft/crew/component.yaml`:

```yaml
name: crew
kind: service
language: go
depends:
  - minecraft/agent/pkg/
tasks:
  build: CGO_ENABLED=0 go build -o /dev/null ./minecraft/crew/...
  test: go test -race ./minecraft/crew/...
  lint: test -z "$(gofmt -l minecraft/crew)" && go vet ./minecraft/crew/...
```

No `release:` block yet. Task 5 adds it with the Dockerfile, because the validator requires the Dockerfile to exist first.

- [ ] **Step 4: Write the failing config tests**

`minecraft/crew/internal/config/config_test.go`:

```go
package config

import (
	"strings"
	"testing"
)

func setRequired(t *testing.T) {
	t.Helper()
	t.Setenv("MC_HOST", "example.invalid")
	t.Setenv("MC_USERNAME", "crew")
}

func TestLoadAppliesDefaults(t *testing.T) {
	setRequired(t)
	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Port != 19132 || cfg.ViewDistance != 8 || cfg.AuthCacheDir != "/data/auth" ||
		cfg.ReconnectMinMs != 5000 || cfg.ReconnectMaxMs != 300000 {
		t.Errorf("defaults = %+v", cfg)
	}
}

func TestLoadRequiresHostAndUsername(t *testing.T) {
	for _, name := range []string{"MC_HOST", "MC_USERNAME"} {
		t.Run(name, func(t *testing.T) {
			setRequired(t)
			t.Setenv(name, "  ")
			_, err := Load()
			if err == nil || !strings.Contains(err.Error(), name) {
				t.Fatalf("err = %v, want one naming %s", err, name)
			}
		})
	}
}

// The status API serves positions. Exposing it on every interface must be a
// choice someone writes down, not what an unset variable does.
func TestStatusAddrDefaultsToLoopback(t *testing.T) {
	setRequired(t)
	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.StatusAddr != "127.0.0.1:8080" {
		t.Errorf("StatusAddr = %q, want 127.0.0.1:8080", cfg.StatusAddr)
	}
}

func TestLoadRejectsBadValues(t *testing.T) {
	cases := map[string]string{
		"MC_VIEW_DISTANCE": "256",
		"MC_PORT":          "70000",
		"STATUS_ADDR":      "8080",
	}
	for name, value := range cases {
		t.Run(name, func(t *testing.T) {
			setRequired(t)
			t.Setenv(name, value)
			_, err := Load()
			if err == nil || !strings.Contains(err.Error(), name) {
				t.Fatalf("%s=%s: err = %v, want one naming %s", name, value, err, name)
			}
		})
	}
}

func TestLoadRejectsZeroViewDistance(t *testing.T) {
	setRequired(t)
	t.Setenv("MC_VIEW_DISTANCE", "0")
	if _, err := Load(); err == nil {
		t.Fatal("MC_VIEW_DISTANCE=0 was accepted")
	}
}

func TestLoadRejectsInvertedBackoff(t *testing.T) {
	setRequired(t)
	t.Setenv("RECONNECT_MIN_MS", "9000")
	t.Setenv("RECONNECT_MAX_MS", "1000")
	_, err := Load()
	if err == nil || !strings.Contains(err.Error(), "RECONNECT_MIN_MS") {
		t.Fatalf("err = %v, want one naming RECONNECT_MIN_MS", err)
	}
}
```

- [ ] **Step 5: Run to confirm they fail**

Run: `go test ./minecraft/crew/internal/config/`
Expected: FAIL, `undefined: Load`.

- [ ] **Step 6: Implement config**

`minecraft/crew/internal/config/config.go`:

```go
// Package config reads crew's settings from the environment, bounded at the
// widths the protocol and the OS will actually accept.
package config

import (
	"errors"
	"fmt"
	"net"
	"os"
	"strconv"
	"strings"
)

const (
	maxPort      = 65535
	maxBackoffMs = 3_600_000
	// A worker needs the chunks it is working in, not a farm's radius. 32 is
	// generous for that and far inside RequestChunkRadius's uint8.
	maxViewDistance uint8 = 32
)

type Config struct {
	Host     string
	Port     int
	Username string

	ViewDistance uint8

	AuthCacheDir   string
	ReconnectMinMs int
	ReconnectMaxMs int

	StatusAddr string
}

func Load() (Config, error) {
	host, err := required("MC_HOST")
	if err != nil {
		return Config{}, err
	}
	username, err := required("MC_USERNAME")
	if err != nil {
		return Config{}, err
	}
	port, err := intInRange("MC_PORT", 19132, 1, maxPort)
	if err != nil {
		return Config{}, err
	}
	viewDistance, err := uint8InRange("MC_VIEW_DISTANCE", 8, 1, maxViewDistance)
	if err != nil {
		return Config{}, err
	}
	reconnectMin, err := intInRange("RECONNECT_MIN_MS", 5000, 1, maxBackoffMs)
	if err != nil {
		return Config{}, err
	}
	reconnectMax, err := intInRange("RECONNECT_MAX_MS", 300000, 1, maxBackoffMs)
	if err != nil {
		return Config{}, err
	}
	if reconnectMin > reconnectMax {
		return Config{}, fmt.Errorf("RECONNECT_MIN_MS (%d) exceeds RECONNECT_MAX_MS (%d)", reconnectMin, reconnectMax)
	}
	statusAddr := stringDefault("STATUS_ADDR", "127.0.0.1:8080")
	if _, _, err := net.SplitHostPort(statusAddr); err != nil {
		return Config{}, fmt.Errorf("STATUS_ADDR %q: %w", statusAddr, err)
	}

	return Config{
		Host:           host,
		Port:           port,
		Username:       username,
		ViewDistance:   viewDistance,
		AuthCacheDir:   stringDefault("AUTH_CACHE_DIR", "/data/auth"),
		ReconnectMinMs: reconnectMin,
		ReconnectMaxMs: reconnectMax,
		StatusAddr:     statusAddr,
	}, nil
}

func required(name string) (string, error) {
	v := strings.TrimSpace(os.Getenv(name))
	if v == "" {
		return "", errors.New(name + " is required")
	}
	return v, nil
}

func stringDefault(name, def string) string {
	if v := strings.TrimSpace(os.Getenv(name)); v != "" {
		return v
	}
	return def
}

func intInRange(name string, def, min, max int) (int, error) {
	raw := strings.TrimSpace(os.Getenv(name))
	if raw == "" {
		return def, nil
	}
	n, err := strconv.Atoi(raw)
	if err != nil || n < min || n > max {
		return 0, fmt.Errorf("%s=%q: want an integer in [%d, %d]", name, raw, min, max)
	}
	return n, nil
}

// Parsed at uint8 width so a value the packet cannot carry is refused here
// rather than wrapping on the way in.
func uint8InRange(name string, def, min, max uint8) (uint8, error) {
	raw := strings.TrimSpace(os.Getenv(name))
	if raw == "" {
		return def, nil
	}
	n, err := strconv.ParseUint(raw, 10, 8)
	if err != nil || uint8(n) < min || uint8(n) > max {
		return 0, fmt.Errorf("%s=%q: want an integer in [%d, %d]", name, raw, min, max)
	}
	return uint8(n), nil
}
```

- [ ] **Step 7: Run the tests and the component validator**

Run: `go test -race ./minecraft/crew/... && go run ./tools/components validate`
Expected: PASS, and the validator exits 0.

- [ ] **Step 8: Commit**

```bash
/usr/bin/git add .gitignore minecraft/crew
/usr/bin/git commit -m "feat(crew): add the lab world and crew's config"
```

---

### Task 2: Belief tracker

**Files:**
- Create: `minecraft/crew/internal/belief/belief.go`
- Test: `minecraft/crew/internal/belief/belief_test.go`

**Interfaces:**
- Consumes: nothing from earlier tasks.
- Produces:
  - `type Snapshot struct { Spawned bool; Dead bool; Dimension int32; Position [3]float32; Health float32; HealthKnown bool; UpdatedAt time.Time }`, with JSON tags `spawned, dead, dimension, position, health, health_known, updated_at`
  - `belief.New(now func() time.Time) *Tracker`
  - `(*Tracker).Spawn(runtimeID uint64, pos mgl32.Vec3, dimension int32)`
  - `(*Tracker).Apply(pk packet.Packet)`
  - `(*Tracker).SetDead(dead bool)`
  - `(*Tracker).Reset()`
  - `(*Tracker).Snapshot() Snapshot`

- [ ] **Step 1: Write the failing tests**

`minecraft/crew/internal/belief/belief_test.go`:

```go
package belief

import (
	"testing"
	"time"

	"github.com/go-gl/mathgl/mgl32"
	"github.com/sandertv/gophertunnel/minecraft/protocol"
	"github.com/sandertv/gophertunnel/minecraft/protocol/packet"
)

const self uint64 = 7

func fixedClock() func() time.Time {
	t0 := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	return func() time.Time { return t0 }
}

func spawned(t *testing.T) *Tracker {
	t.Helper()
	tr := New(fixedClock())
	tr.Spawn(self, mgl32.Vec3{1, 2, 3}, 0)
	return tr
}

func TestSpawnRecordsTheStartingState(t *testing.T) {
	s := spawned(t).Snapshot()
	if !s.Spawned || s.Dead || s.Position != [3]float32{1, 2, 3} || s.Dimension != 0 {
		t.Errorf("snapshot = %+v", s)
	}
}

func TestOwnMovementUpdatesPosition(t *testing.T) {
	tr := spawned(t)
	tr.Apply(&packet.MovePlayer{EntityRuntimeID: self, Position: mgl32.Vec3{4, 5, 6}})
	if got := tr.Snapshot().Position; got != [3]float32{4, 5, 6} {
		t.Errorf("position = %v, want [4 5 6]", got)
	}
}

func TestOtherEntitiesDoNotMoveTheUnit(t *testing.T) {
	tr := spawned(t)
	tr.Apply(&packet.MovePlayer{EntityRuntimeID: self + 1, Position: mgl32.Vec3{9, 9, 9}})
	tr.Apply(&packet.UpdateAttributes{EntityRuntimeID: self + 1, Attributes: []protocol.Attribute{
		{AttributeValue: protocol.AttributeValue{Name: "minecraft:health", Value: 1}},
	}})
	s := tr.Snapshot()
	if s.Position != [3]float32{1, 2, 3} || s.HealthKnown {
		t.Errorf("another entity changed the unit: %+v", s)
	}
}

func TestHealthComesFromOwnAttributes(t *testing.T) {
	tr := spawned(t)
	tr.Apply(&packet.UpdateAttributes{EntityRuntimeID: self, Attributes: []protocol.Attribute{
		{AttributeValue: protocol.AttributeValue{Name: "minecraft:movement", Value: 0.1}},
		{AttributeValue: protocol.AttributeValue{Name: "minecraft:health", Value: 14}},
	}})
	s := tr.Snapshot()
	if !s.HealthKnown || s.Health != 14 {
		t.Errorf("health = %v known=%v, want 14 known", s.Health, s.HealthKnown)
	}
}

func TestChangeDimensionMovesTheUnit(t *testing.T) {
	tr := spawned(t)
	tr.Apply(&packet.ChangeDimension{Dimension: 1, Position: mgl32.Vec3{8, 64, 8}})
	s := tr.Snapshot()
	if s.Dimension != 1 || s.Position != [3]float32{8, 64, 8} {
		t.Errorf("snapshot = %+v", s)
	}
}

func TestResetForgetsTheSession(t *testing.T) {
	tr := spawned(t)
	tr.SetDead(true)
	tr.Reset()
	s := tr.Snapshot()
	if s.Spawned || s.Dead || s.HealthKnown {
		t.Errorf("after reset = %+v, want an unspawned zero state", s)
	}
}

// Before Spawn there is no runtime ID to match against, so nothing may be
// attributed to the unit -- runtime ID 0 is a real ID on some servers.
func TestPacketsBeforeSpawnAreIgnored(t *testing.T) {
	tr := New(fixedClock())
	tr.Apply(&packet.MovePlayer{EntityRuntimeID: 0, Position: mgl32.Vec3{9, 9, 9}})
	if s := tr.Snapshot(); s.Spawned || s.Position != [3]float32{} {
		t.Errorf("snapshot = %+v, want untouched", s)
	}
}
```

- [ ] **Step 2: Run to confirm they fail**

Run: `go test ./minecraft/crew/internal/belief/`
Expected: FAIL, `undefined: New`.

- [ ] **Step 3: Implement**

`minecraft/crew/internal/belief/belief.go`:

```go
// Package belief is what crew knows about itself, built only from packets the
// server sent. It is a belief rather than the truth: a packet the server never
// sends leaves it stale, which is why every snapshot carries UpdatedAt.
package belief

import (
	"sync"
	"time"

	"github.com/go-gl/mathgl/mgl32"
	"github.com/sandertv/gophertunnel/minecraft/protocol/packet"
)

const healthAttribute = "minecraft:health"

// Snapshot is a copy, safe to hand to another goroutine.
type Snapshot struct {
	Spawned   bool  `json:"spawned"`
	Dead      bool  `json:"dead"`
	Dimension int32 `json:"dimension"`
	// As the server reports it. Bedrock places players at eye height, so this
	// is not the block the unit stands on.
	Position    [3]float32 `json:"position"`
	Health      float32    `json:"health"`
	HealthKnown bool       `json:"health_known"`
	UpdatedAt   time.Time  `json:"updated_at"`
}

// Tracker is written by the session's read loop and read by the status API.
type Tracker struct {
	mu   sync.RWMutex
	now  func() time.Time
	self uint64
	s    Snapshot
}

func New(now func() time.Time) *Tracker { return &Tracker{now: now} }

func (t *Tracker) Spawn(runtimeID uint64, pos mgl32.Vec3, dimension int32) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.self = runtimeID
	t.s = Snapshot{Spawned: true, Dimension: dimension, Position: pos, UpdatedAt: t.now()}
}

func (t *Tracker) Apply(pk packet.Packet) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if !t.s.Spawned {
		return
	}
	switch p := pk.(type) {
	case *packet.MovePlayer:
		if p.EntityRuntimeID != t.self {
			return
		}
		t.s.Position = p.Position
	case *packet.UpdateAttributes:
		if p.EntityRuntimeID != t.self {
			return
		}
		for _, a := range p.Attributes {
			if a.Name == healthAttribute {
				t.s.Health, t.s.HealthKnown = a.Value, true
			}
		}
	case *packet.ChangeDimension:
		t.s.Dimension, t.s.Position = p.Dimension, p.Position
	default:
		return
	}
	t.s.UpdatedAt = t.now()
}

func (t *Tracker) SetDead(dead bool) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.s.Dead = dead
	t.s.UpdatedAt = t.now()
}

// Reset is called when a session ends, so a reader never mistakes the last
// session's state for a live one.
func (t *Tracker) Reset() {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.self = 0
	t.s = Snapshot{UpdatedAt: t.now()}
}

func (t *Tracker) Snapshot() Snapshot {
	t.mu.RLock()
	defer t.mu.RUnlock()
	return t.s
}
```

Note: `t.s.Position = p.Position` assigns an `mgl32.Vec3` (which is `[3]float32`) to a `[3]float32`. That compiles only because `Vec3`'s underlying type is `[3]float32`. If the compiler rejects it, use `[3]float32(p.Position)`.

- [ ] **Step 4: Run the tests (with the race detector) and tidy modules**

Run: `go mod tidy && go test -race ./minecraft/crew/...`
Expected: PASS. `go.mod` may move `github.com/go-gl/mathgl` from indirect to direct. That is the only expected `go.mod` change.

- [ ] **Step 5: Commit**

```bash
/usr/bin/git add go.mod go.sum minecraft/crew/internal/belief
/usr/bin/git commit -m "feat(crew): track the unit's own position, health and life from packets"
```

---

### Task 3: Status API

**Files:**
- Create: `minecraft/crew/internal/statusapi/statusapi.go`
- Test: `minecraft/crew/internal/statusapi/statusapi_test.go`

**Interfaces:**
- Consumes: `belief.Snapshot` (Task 2).
- Produces: `statusapi.Handler(src Source, version string) http.Handler`, where `type Source interface { Snapshot() belief.Snapshot }`. Routes: `GET /healthz`, `GET /readyz`, `GET /v1/state`. The `/v1/state` body is `{"version": string, "state": belief.Snapshot}`.

- [ ] **Step 1: Write the failing tests**

`minecraft/crew/internal/statusapi/statusapi_test.go`:

```go
package statusapi

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/jdwillmsen/gameops/minecraft/crew/internal/belief"
)

type fixed belief.Snapshot

func (f fixed) Snapshot() belief.Snapshot { return belief.Snapshot(f) }

func get(t *testing.T, h http.Handler, path string) *httptest.ResponseRecorder {
	t.Helper()
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil))
	return rec
}

func TestHealthzIsAlwaysOK(t *testing.T) {
	if rec := get(t, Handler(fixed{}, "dev"), "/healthz"); rec.Code != http.StatusOK {
		t.Errorf("healthz = %d", rec.Code)
	}
}

func TestReadyzFollowsSpawn(t *testing.T) {
	if rec := get(t, Handler(fixed{}, "dev"), "/readyz"); rec.Code != http.StatusServiceUnavailable {
		t.Errorf("unspawned readyz = %d, want 503", rec.Code)
	}
	if rec := get(t, Handler(fixed{Spawned: true}, "dev"), "/readyz"); rec.Code != http.StatusOK {
		t.Errorf("spawned readyz = %d, want 200", rec.Code)
	}
}

// A dead unit is connected and spawned but can do no work, and nothing else
// outside the process can tell.
func TestReadyzIsUnavailableWhileDead(t *testing.T) {
	if rec := get(t, Handler(fixed{Spawned: true, Dead: true}, "dev"), "/readyz"); rec.Code != http.StatusServiceUnavailable {
		t.Errorf("dead readyz = %d, want 503", rec.Code)
	}
}

func TestStateReturnsTheSnapshotAndVersion(t *testing.T) {
	rec := get(t, Handler(fixed{Spawned: true, Health: 20, HealthKnown: true}, "1.2.3"), "/v1/state")
	if rec.Code != http.StatusOK || rec.Header().Get("Content-Type") != "application/json" {
		t.Fatalf("state = %d %q", rec.Code, rec.Header().Get("Content-Type"))
	}
	var body struct {
		Version string          `json:"version"`
		State   belief.Snapshot `json:"state"`
	}
	if err := json.NewDecoder(rec.Body).Decode(&body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if body.Version != "1.2.3" || !body.State.Spawned || body.State.Health != 20 {
		t.Errorf("body = %+v", body)
	}
}

func TestWritesAreRefused(t *testing.T) {
	rec := httptest.NewRecorder()
	Handler(fixed{}, "dev").ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/v1/state", nil))
	if rec.Code != http.StatusMethodNotAllowed {
		t.Errorf("POST /v1/state = %d, want 405", rec.Code)
	}
}
```

- [ ] **Step 2: Run to confirm they fail**

Run: `go test ./minecraft/crew/internal/statusapi/`
Expected: FAIL, `undefined: Handler`.

- [ ] **Step 3: Implement**

`minecraft/crew/internal/statusapi/statusapi.go`:

```go
// Package statusapi serves what crew believes about itself. It is read-only
// and unauthenticated, so it must only be reachable from where crew-control
// and the probes run: config defaults its address to loopback.
package statusapi

import (
	"encoding/json"
	"net/http"

	"github.com/jdwillmsen/gameops/minecraft/crew/internal/belief"
)

type Source interface {
	Snapshot() belief.Snapshot
}

type stateBody struct {
	Version string          `json:"version"`
	State   belief.Snapshot `json:"state"`
}

func Handler(src Source, version string) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("ok\n"))
	})
	mux.HandleFunc("GET /readyz", func(w http.ResponseWriter, _ *http.Request) {
		s := src.Snapshot()
		if !s.Spawned || s.Dead {
			http.Error(w, "not in the world", http.StatusServiceUnavailable)
			return
		}
		_, _ = w.Write([]byte("ok\n"))
	})
	mux.HandleFunc("GET /v1/state", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(stateBody{Version: version, State: src.Snapshot()})
	})
	return mux
}
```

- [ ] **Step 4: Run the tests**

Run: `go test -race ./minecraft/crew/...`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
/usr/bin/git add minecraft/crew/internal/statusapi
/usr/bin/git commit -m "feat(crew): serve the unit's state, health and readiness over HTTP"
```

---

### Task 4: The crew command

**Files:**
- Create: `minecraft/crew/cmd/crew/main.go`
- Create: `minecraft/crew/cmd/crew/loop.go`
- Test: `minecraft/crew/cmd/crew/loop_test.go`
- Test: `minecraft/crew/cmd/crew/session_test.go`

**Interfaces:**
- Consumes: `config.Load`, `belief.New/Spawn/Apply/SetDead/Reset/Snapshot`, `statusapi.Handler`. From `minecraft/agent/pkg`: `logging.New(level string) *logging.Logger` and `logging.Fields`, `mcauth.NewFileStore(dir, username string) (*mcauth.FileStore, error)`, `mcauth.TokenSource(ctx, store, out io.Writer, opts ...mcauth.Option) (oauth2.TokenSource, error)`, `mcdial.Dial(ctx, minecraft.Dialer, addr string, connect, spawn time.Duration) (*minecraft.Conn, error)`, `mcproto.Negotiate(ctx, addr string, onSpoof func(mcproto.Advertisement)) minecraft.Protocol`, `skin.For(name string) skin.Skin`, `liveness.New(runtimeID uint64) *liveness.Respawner` with `Handle(pk, w) (bool, error)` and `Dead() bool`.
- Produces: the `crew` binary. Task 5 builds it as `./minecraft/crew/cmd/crew` and sets `main.version` through `-ldflags`.

Before writing, read `minecraft/afkbot/cmd/bot/main.go`. This command is the same shape with the presence gate removed and belief added. Keep its comments' reasoning where the code is the same. Do not copy comments about farms or chunk-holding.

- [ ] **Step 1: Write the failing loop tests**

`minecraft/crew/cmd/crew/loop_test.go`:

```go
package main

import (
	"context"
	"errors"
	"testing"
	"time"
)

// instant makes every wait elapse at once, so the loop's control flow is
// tested without real sleeps.
func instant(time.Duration) <-chan time.Time {
	c := make(chan time.Time, 1)
	c <- time.Time{}
	return c
}

func TestBackoffDoublesToTheCeilingAndResetsAfterAStableSession(t *testing.T) {
	min, max := time.Second, 8*time.Second
	if got := backoff(min, time.Second, min, max); got != 2*time.Second {
		t.Errorf("short session: %v, want 2s", got)
	}
	if got := backoff(8*time.Second, time.Second, min, max); got != max {
		t.Errorf("at ceiling: %v, want %v", got, max)
	}
	if got := backoff(8*time.Second, stableSession, min, max); got != min {
		t.Errorf("stable session: %v, want reset to %v", got, min)
	}
}

func TestJitterStaysWithinHalfToFull(t *testing.T) {
	for i := 0; i < 1000; i++ {
		if j := jitter(10 * time.Second); j < 5*time.Second || j > 10*time.Second {
			t.Fatalf("jitter = %v, want within [5s, 10s]", j)
		}
	}
}

func TestLoopReconnectsUntilCancelled(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	calls := 0
	connect := func(context.Context) error {
		calls++
		if calls == 3 {
			cancel()
		}
		return errors.New("kicked")
	}
	runConnectLoop(ctx, loopOptions{minDelay: time.Millisecond, maxDelay: time.Millisecond, after: instant}, connect, func() {}, discardLog{})
	if calls != 3 {
		t.Errorf("connect called %d times, want 3", calls)
	}
}

func TestLoopResetsBeliefWhenASessionEnds(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	resets := 0
	connect := func(context.Context) error { cancel(); return nil }
	runConnectLoop(ctx, loopOptions{minDelay: time.Millisecond, maxDelay: time.Millisecond, after: instant}, connect, func() { resets++ }, discardLog{})
	if resets != 1 {
		t.Errorf("onEnd called %d times, want 1", resets)
	}
}

type discardLog struct{}

func (discardLog) Info(string, map[string]any)  {}
func (discardLog) Error(string, map[string]any) {}
```

- [ ] **Step 2: Run to confirm they fail**

Run: `go test ./minecraft/crew/cmd/crew/`
Expected: FAIL, `undefined: backoff`.

- [ ] **Step 3: Implement the loop**

`minecraft/crew/cmd/crew/loop.go`:

```go
package main

import (
	"context"
	"math/rand"
	"time"
)

// A session lasting at least this long is treated as healthy and resets the
// reconnect backoff. Without it, a unit that connects and is immediately
// kicked retries as eagerly as one recovering from a momentary blip.
const stableSession = 60 * time.Second

type loopLogger interface {
	Info(event string, fields map[string]any)
	Error(event string, fields map[string]any)
}

type loopOptions struct {
	minDelay, maxDelay time.Duration
	// after is time.After outside tests.
	after func(time.Duration) <-chan time.Time
}

// runConnectLoop keeps crew connected for the life of the process. It
// reconnects rather than exiting, because exiting moves the retry loop into
// Kubernetes: a coarser backoff, a fresh pod, and a token reload on every
// attempt.
func runConnectLoop(ctx context.Context, opt loopOptions, connect func(context.Context) error, onEnd func(), log loopLogger) {
	delay := opt.minDelay
	first := true
	for ctx.Err() == nil {
		if !first {
			wait := jitter(delay)
			log.Info("reconnecting", map[string]any{"in_ms": wait.Milliseconds()})
			select {
			case <-opt.after(wait):
			case <-ctx.Done():
				return
			}
		}
		first = false

		started := time.Now()
		err := connect(ctx)
		lasted := time.Since(started)
		onEnd()
		if ctx.Err() != nil {
			return
		}
		if err != nil {
			log.Error("session_error", map[string]any{"error": err.Error(), "session_lasted_ms": lasted.Milliseconds()})
		} else {
			log.Info("session_ended", map[string]any{"session_lasted_ms": lasted.Milliseconds()})
		}
		delay = backoff(delay, lasted, opt.minDelay, opt.maxDelay)
	}
}

func backoff(current, lasted, min, max time.Duration) time.Duration {
	if lasted >= stableSession {
		return min
	}
	if next := current * 2; next < max {
		return next
	}
	return max
}

// jitter spreads reconnects so a server restart does not bring every unit
// back in lockstep, at the moment the server can least take it.
func jitter(d time.Duration) time.Duration {
	if d <= 0 {
		return 0
	}
	return d/2 + time.Duration(rand.Int63n(int64(d/2)+1))
}
```

`*logging.Logger` does not satisfy `loopLogger`: its methods take the named type `logging.Fields`, and Go method sets do not unify named and unnamed map types. `main.go` (Step 6) bridges it with `logAdapter`. The loop stays free of the logging package, so tests can pass `discardLog{}`.

- [ ] **Step 4: Run the loop tests**

Run: `go test -race ./minecraft/crew/cmd/crew/ -run 'Backoff|Jitter|Loop'`
Expected: PASS.

- [ ] **Step 5: Write the failing session log test**

The session's log fields are built by one function, so the "no positions in logs" rule can be tested without a server.

`minecraft/crew/cmd/crew/session_test.go`:

```go
package main

import (
	"strings"
	"testing"
)

func TestSessionLogFieldsCarryNoPosition(t *testing.T) {
	f := spawnedFields("2535400000000000", 8)
	for k := range f {
		switch strings.ToLower(k) {
		case "x", "y", "z", "pos", "position", "coords", "coordinates", "dimension":
			t.Errorf("spawn log carries %q; positions never go to logs", k)
		}
	}
	if f["xuid"] == nil || f["view_distance"] == nil {
		t.Errorf("fields = %v, want xuid and view_distance", f)
	}
}
```

- [ ] **Step 6: Implement main.go**

`minecraft/crew/cmd/crew/main.go`:

```go
// Command crew is a programmable worker unit on a Bedrock server. In this
// first form it only joins, keeps track of itself and reports what it
// believes; skills arrive with crew-control.
//
// It has no chat and no model client, by design: crew's behaviour is the code
// it ships (docs/specs/2026-10-14-crew-design.md).
package main

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"syscall"
	"time"

	"github.com/jdwillmsen/gameops/minecraft/agent/pkg/liveness"
	"github.com/jdwillmsen/gameops/minecraft/agent/pkg/logging"
	"github.com/jdwillmsen/gameops/minecraft/agent/pkg/mcauth"
	"github.com/jdwillmsen/gameops/minecraft/agent/pkg/mcdial"
	"github.com/jdwillmsen/gameops/minecraft/agent/pkg/mcproto"
	"github.com/jdwillmsen/gameops/minecraft/agent/pkg/skin"
	"github.com/jdwillmsen/gameops/minecraft/crew/internal/belief"
	"github.com/jdwillmsen/gameops/minecraft/crew/internal/config"
	"github.com/jdwillmsen/gameops/minecraft/crew/internal/statusapi"
	"github.com/sandertv/gophertunnel/minecraft"
	"github.com/sandertv/gophertunnel/minecraft/protocol/login"
	"github.com/sandertv/gophertunnel/minecraft/protocol/packet"
	"golang.org/x/oauth2"
)

const (
	connectTimeout = 30 * time.Second
	// Far longer than connectTimeout: a freshly started server loads its
	// world for the first player in, and abandoning a login the server would
	// have completed is worse than waiting.
	spawnTimeout = 90 * time.Second
)

var version = "dev"

func main() {
	log := logging.New(os.Getenv("LOG_LEVEL"))

	cfg, err := config.Load()
	if err != nil {
		log.Error("config_invalid", logging.Fields{"error": err.Error()})
		os.Exit(1)
	}

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	store, err := mcauth.NewFileStore(cfg.AuthCacheDir, cfg.Username)
	if err != nil {
		log.Error("auth_store_invalid", logging.Fields{"error": err.Error()})
		os.Exit(1)
	}
	// The device code is printed by design: a first start against an empty
	// cache needs a human, and this is the only place the code appears.
	ts, err := mcauth.TokenSource(ctx, store, os.Stdout)
	if err != nil {
		log.Error("auth_failed", logging.Fields{"error": err.Error()})
		os.Exit(1)
	}

	tracker := belief.New(time.Now)
	srv := &http.Server{Addr: cfg.StatusAddr, Handler: statusapi.Handler(tracker, version), ReadHeaderTimeout: 5 * time.Second}
	go func() {
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Error("status_api_failed", logging.Fields{"error": err.Error()})
			stop()
		}
	}()
	defer func() { _ = srv.Shutdown(context.Background()) }()

	runConnectLoop(ctx,
		loopOptions{
			minDelay: time.Duration(cfg.ReconnectMinMs) * time.Millisecond,
			maxDelay: time.Duration(cfg.ReconnectMaxMs) * time.Millisecond,
			after:    time.After,
		},
		func(ctx context.Context) error { return session(ctx, cfg, ts, tracker, log) },
		tracker.Reset,
		logAdapter{log},
	)
}

type logAdapter struct{ l *logging.Logger }

func (a logAdapter) Info(e string, f map[string]any)  { a.l.Info(e, logging.Fields(f)) }
func (a logAdapter) Error(e string, f map[string]any) { a.l.Error(e, logging.Fields(f)) }

// spawnedFields is the spawn log line. It is a function of its own so a test
// can hold it to the rule that positions never reach a log.
func spawnedFields(xuid string, viewDistance uint8) logging.Fields {
	return logging.Fields{"xuid": xuid, "view_distance": viewDistance}
}

func session(ctx context.Context, cfg config.Config, ts oauth2.TokenSource, tracker *belief.Tracker, log *logging.Logger) error {
	// A headless client uploads no skin of its own, and without one the unit
	// joins as a black silhouette under a fresh skin ID every connect.
	unitSkin := skin.For(cfg.Username)
	addr := net.JoinHostPort(cfg.Host, strconv.Itoa(cfg.Port))

	// The server upgrades itself on restart; announcing the server's own
	// protocol number keeps a release that changed no packets from kicking
	// crew before login.
	proto := mcproto.Negotiate(ctx, addr, func(ad mcproto.Advertisement) {
		log.Warn("protocol_spoofed", logging.Fields{
			"compiled_protocol":   minecraft.DefaultProtocol.ID(),
			"advertised_protocol": ad.Protocol,
			"server_version":      ad.Version,
		})
	})

	conn, err := mcdial.Dial(ctx, minecraft.Dialer{
		TokenSource: ts,
		Protocol:    proto,
		ClientData: login.ClientData{
			SkinID:          unitSkin.ID,
			SkinData:        unitSkin.Data,
			SkinImageWidth:  unitSkin.Width,
			SkinImageHeight: unitSkin.Height,
		},
	}, addr, connectTimeout, spawnTimeout)
	if err != nil {
		return fmt.Errorf("dial: %w", err)
	}
	defer func() { _ = conn.Close() }()

	// ReadPacket does not watch ctx, so shutdown would otherwise wait for the
	// server's next packet.
	stopClose := context.AfterFunc(ctx, func() { _ = conn.Close() })
	defer stopClose()

	if err := conn.DoSpawnContext(ctx); err != nil {
		return fmt.Errorf("spawn: %w", err)
	}
	gd := conn.GameData()
	tracker.Spawn(gd.EntityRuntimeID, gd.PlayerPosition, gd.Dimension)
	log.Info("spawned", spawnedFields(conn.IdentityData().XUID, cfg.ViewDistance))

	if err := conn.WritePacket(&packet.RequestChunkRadius{ChunkRadius: int32(cfg.ViewDistance), MaxChunkRadius: cfg.ViewDistance}); err != nil {
		return fmt.Errorf("request chunk radius: %w", err)
	}

	respawner := liveness.New(gd.EntityRuntimeID)
	for {
		pk, err := conn.ReadPacket()
		if err != nil {
			if ctx.Err() != nil {
				return nil
			}
			return fmt.Errorf("read: %w", err)
		}
		tracker.Apply(pk)

		// A dead unit stays connected and spawned; only the respawn
		// handshake shows it, so belief is told explicitly.
		handled, err := respawner.Handle(pk, conn)
		if err != nil {
			log.Error("respawn_failed", logging.Fields{"error": err.Error()})
			continue
		}
		if !handled {
			continue
		}
		tracker.SetDead(respawner.Dead())
		if respawner.Dead() {
			log.Info("died", nil)
			continue
		}
		log.Info("respawned", nil)
		// The server drops the radius when the player is replaced.
		if err := conn.WritePacket(&packet.RequestChunkRadius{ChunkRadius: int32(cfg.ViewDistance), MaxChunkRadius: cfg.ViewDistance}); err != nil {
			return fmt.Errorf("request chunk radius after respawn: %w", err)
		}
	}
}
```

If any `agent/pkg` signature differs from the Interfaces block, the package wins. Adapt the call and note the difference in the PR description. Do not edit `agent/pkg`.

- [ ] **Step 7: Run everything, lint, and build**

Run: `go test -race ./minecraft/crew/... && test -z "$(gofmt -l minecraft/crew)" && go vet ./minecraft/crew/... && CGO_ENABLED=0 go build -o /dev/null ./minecraft/crew/...`
Expected: all PASS, with no gofmt output.

- [ ] **Step 8: Commit**

```bash
/usr/bin/git add minecraft/crew/cmd
/usr/bin/git commit -m "feat(crew): join a Bedrock server, track itself and reconnect"
```

---

### Task 5: Image, lab wiring, docs, and live verification

**Files:**
- Create: `minecraft/crew/Dockerfile`
- Modify: `minecraft/crew/lab/compose.yaml` (add the `crew` service)
- Create: `minecraft/crew/README.md`
- Create: `minecraft/crew/docs/decisions.md`

**Interfaces:**
- Consumes: the `crew` binary from Task 4. The `-X main.version` ldflag sets `version`.

- [ ] **Step 1: Write the Dockerfile**

Copy `minecraft/afkbot/Dockerfile` exactly, including both digest pins and the `USER nonroot:nonroot` line with its comment. Change only:
- the build target: `-o /out/crew ./minecraft/crew/cmd/crew`
- `COPY --from=build /out/crew /crew` and `ENTRYPOINT ["/crew"]`
- the comment on the `COPY . .` stage: crew imports the agent's `pkg/` rather than carrying copies.
- add `EXPOSE 8080` above `VOLUME`.

- [ ] **Step 2: Add crew to the lab compose**

Append under `services:` in `minecraft/crew/lab/compose.yaml`:

```yaml
  crew-1:
    build:
      context: ../../..
      dockerfile: minecraft/crew/Dockerfile
    depends_on: [lab]
    environment:
      MC_HOST: lab
      MC_PORT: "19132"
      MC_USERNAME: ${CREW_USERNAME:?set CREW_USERNAME in minecraft/crew/lab/.env}
      # Bound wide only inside the compose network; the published port below
      # is loopback-only on the host.
      STATUS_ADDR: 0.0.0.0:8080
    ports:
      - "127.0.0.1:8080:8080"
    volumes:
      - crew-1-auth:/data
```

Add `crew-1-auth:` under `volumes:`.

- [ ] **Step 3: Live check: join**

Run:
```bash
cd minecraft/crew/lab && cp -n .env.example .env   # owner fills CREW_USERNAME
docker compose up -d --build && docker compose logs -f crew-1
```
Expected: a device-code prompt on the first run (the owner signs in once), then a `spawned` JSON line. Then:
`curl -s localhost:8080/readyz` → `ok`; `curl -s localhost:8080/v1/state` → `"spawned":true` with a non-zero `updated_at`.

- [ ] **Step 4: Live check: death (lab cheats)**

Run: `docker compose exec lab send-command kill "$(grep CREW_USERNAME .env | cut -d= -f2)"` and then immediately `curl -s -o /dev/null -w '%{http_code}\n' localhost:8080/readyz`.
Expected: `503` while dead (it may be brief), `died` and `respawned` lines in the crew logs, and `200` again within a few seconds.

- [ ] **Step 5: Live check: server restart**

Run: `docker compose restart lab`, then poll `curl -s -o /dev/null -w '%{http_code}\n' localhost:8080/readyz` every 5s.
Expected: `503` while the server is down, `session_error` and `reconnecting` in the logs, and `200` again after the server is back. No crash-loop.

- [ ] **Step 6: Live check: no positions in logs**

Run: `docker compose logs crew-1 | grep -Ei '"(x|y|z|pos|position|coords?|coordinates|dimension)"' ; echo "matches=$?"`
Expected: `matches=1` (grep found nothing).

- [ ] **Step 7: Write the README and the decisions record**

`minecraft/crew/README.md` covers: what crew is (one paragraph, pointing at the design spec), the environment variables table (every `config.Load` variable with its default), running the lab (Steps 3–5 condensed), and the status API routes. No gamertags.

`minecraft/crew/docs/decisions.md`, in afkbot's dated format, has three entries dated 2026-10-14:
- "reuses the agent's pkg/ rather than copying": D9's reasoning.
- "no presence client yet": crew-control will own crew parking (D5), and the lab has no agent.
- "status API defaults to loopback": positions are seed-equivalent on FWB.

- [ ] **Step 7b: Publish the image**

Add the release block to `minecraft/crew/component.yaml`, matching afkbot's:

```yaml
release:
  tag: crew
  artifacts: [image]
  image:
    name: minecraft-crew
    description: Programmable Minecraft Bedrock worker unit that joins a server and reports its own state
    short_description: Programmable Minecraft Bedrock worker unit
```

Create `minecraft/crew/README.docker.md`, the image page the validator requires. Model it on `minecraft/afkbot/README.docker.md`: what the image is, its environment variables, the `/data` volume, and port 8080.

- [ ] **Step 8: Validate and commit**

Run: `go run ./tools/components validate && docker build -f minecraft/crew/Dockerfile .`
Expected: both succeed.

```bash
/usr/bin/git add minecraft/crew
/usr/bin/git commit -m "feat(crew): ship crew as an image and run it against the lab"
```

- [ ] **Step 9: Ship**

Run the `/no-mistakes` pipeline. The PR body's *Verified* section lists Steps 3–6 with their observed outputs (no gamertags, no positions). After merge, confirm the release workflow published `ghcr.io/jdwillmsen/minecraft-crew` and note the tag for Task 6. Before opening the PR, check the base branch for a required approval or CODEOWNERS gate in both places it can live: `gh api repos/jdwillmsen/gameops/rules/branches/main` and `.../branches/main/protection`. If either gates, open the PR as `jdwlabs-agent-bot`.

---

### Task 6: Run the lab and crew-1 on the cluster

**Repo:** `jdwillmsen/jdw-deployments`. The branch is `feat/JDW-<n>-crew-lab-chart`, in its own worktree.

**Files:**
- Create: `charts/crew-lab/Chart.yaml`, `charts/crew-lab/values.yaml`, `charts/crew-lab/values-prd.yaml`
- Create: `charts/crew-lab/templates/crew-deployment.yaml`, `crew-pvc.yaml`, `crew-service.yaml`
- Create: `charts/crew-lab/README.md`
- Modify: `argocd/prd/config.yaml` (append one app)

**Interfaces:**
- Consumes: the image `ghcr.io/jdwillmsen/minecraft-crew:<tag>` from Task 5, its env vars (Task 1's `config.Load`), and `/healthz`, `/readyz` and `/v1/state` on port 8080 (Task 3).

Before writing, read `charts/minecraft-fwb/Chart.yaml`, `values.yaml` (server, storage, affinity, NodePort and Service sections), `templates/bot-deployment.yaml`, `templates/bot-pvc.yaml` and `argocd/prd/config.yaml`. Copy their patterns and the reasoning in their comments where it applies. Do not reference FWB's values from the new chart.

- [ ] **Step 1: The lab server as the itzg subchart**

`Chart.yaml` depends on `minecraft-bedrock` 2.9.0 from `https://itzg.github.io/minecraft-server-charts/`, the same as FWB. In `values.yaml` under `minecraft-bedrock:`:
- image `itzg/minecraft-bedrock-server`, the same tag as FWB (`2026.9.2`), with `version: "LATEST"` and a comment saying both move together
- `eula: "TRUE"`, `gameMode: survival`, `difficulty: normal`, `cheats: true`, `onlineMode: true`, `serverName: crew-lab`, `levelName: crew-lab`
- `serviceType: NodePort` with a `nodePort` FWB does not use (check with `grep -rn nodePort charts/`), so the owner can join from a game client and watch
- persistence on `truenas-iscsi`. Copy FWB's single-node affinity and its comment about iSCSI volume migration, but pick the node by `kubectl get nodes` headroom and record the measurement in the comment. Losing the lab world is acceptable, so there is no backup CronJob
- resources and probes copied from FWB's server, scaled down: a lab with one or two players needs a fraction of FWB's memory

- [ ] **Step 2: crew-1 Deployment, auth PVC and Service**

Model these on `bot-deployment.yaml` and `bot-pvc.yaml`:
- Deployment `crew-1`, `replicas: 1`, `strategy: Recreate`. Two pods would sign in as the same account and kick each other.
- env: `MC_HOST` = the lab's in-cluster Service name, `MC_PORT` = the server's container port, `MC_USERNAME` from values, and `STATUS_ADDR=0.0.0.0:8080`
- `livenessProbe` → `/healthz`, `readinessProbe` → `/readyz` on 8080. Readiness can be false for long periods (death, server restart), so nothing may restart the pod on readiness.
- PVC on `truenas-nfs` at `/data` for the token cache, same as the bot's
- the same securityContext as the bot: non-root, read-only root filesystem, all capabilities dropped
- a ClusterIP Service on 8080. No Ingress: `/v1/state` carries positions and stays in-cluster until crew-control adds auth in Wave 3.

- [ ] **Step 3: Render and lint**

Run: `helm dependency build charts/crew-lab && helm lint charts/crew-lab -f charts/crew-lab/values.yaml -f charts/crew-lab/values-prd.yaml && helm template crew-lab charts/crew-lab -f charts/crew-lab/values.yaml -f charts/crew-lab/values-prd.yaml -n jdwillmsen-prd > /tmp/crew-lab.yaml`
Expected: lint passes. `grep -c '^kind:' /tmp/crew-lab.yaml` lists the StatefulSet, Services, Deployment and PVCs, and nothing named `minecraft-fwb`.

- [ ] **Step 4: Register with ArgoCD**

Append to `argocd/prd/config.yaml`:

```yaml
  - name: crew-lab-prd
    namespace: jdwillmsen-prd
    chartPath: charts/crew-lab
    syncWave: "2"
    valueFiles:
      - values.yaml
      - values-prd.yaml
```

Then run the repo's own validation (`.github/workflows` lists it; run the same commands locally) and commit.

- [ ] **Step 5: Ship, then the first sign-in**

Ship through `/no-mistakes`, after the same approval-gate check as Task 5. After ArgoCD syncs, crew-1 prints a Microsoft device code on its first start. The device code is a credential prompt, so an agent must not read it through its own shell. Hand the owner this command for a separate terminal:

```bash
kubectl -n jdwillmsen-prd logs deploy/crew-1 -f | grep -m1 -i 'microsoft.com/link'
```

- [ ] **Step 6: Live checks in the cluster**

After sign-in, from a `kubectl port-forward -n jdwillmsen-prd svc/crew-1 8080:8080`, repeat Task 5's Steps 3–6 against the cluster: join, death (`kubectl exec` into the lab pod and run `send-command kill <gamertag>`), lab restart (`kubectl delete pod` on the lab server pod), and the log grep. Also check that the ArgoCD app `crew-lab-prd` is Synced and Healthy, and that FWB's app was not touched (`argocd app get minecraft-fwb-prd` shows no new sync). Paste the outputs into the Jira ticket, without gamertags or positions.
