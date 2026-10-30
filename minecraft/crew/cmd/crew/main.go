// Command crew is a programmable worker unit on a Bedrock server. In this
// first form it only joins, keeps track of itself and reports what it
// believes; skills arrive with crew-control.
//
// It has no chat and no model client, by design: crew's behaviour is the code
// it ships (docs/specs/2026-10-14-crew-design.md).
package main

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"math"
	"math/rand"
	"net"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/google/uuid"
	"github.com/jdwillmsen/gameops/minecraft/agent/pkg/liveness"
	"github.com/jdwillmsen/gameops/minecraft/agent/pkg/logging"
	"github.com/jdwillmsen/gameops/minecraft/agent/pkg/mcdial"
	"github.com/jdwillmsen/gameops/minecraft/agent/pkg/mcproto"
	"github.com/jdwillmsen/gameops/minecraft/agent/pkg/skin"
	"github.com/jdwillmsen/gameops/minecraft/crew/internal/arena"
	"github.com/jdwillmsen/gameops/minecraft/crew/internal/belief"
	"github.com/jdwillmsen/gameops/minecraft/crew/internal/camera"
	"github.com/jdwillmsen/gameops/minecraft/crew/internal/config"
	"github.com/jdwillmsen/gameops/minecraft/crew/internal/control"
	"github.com/jdwillmsen/gameops/minecraft/crew/internal/life"
	"github.com/jdwillmsen/gameops/minecraft/crew/internal/sessionid"
	"github.com/jdwillmsen/gameops/minecraft/crew/internal/skill"
	"github.com/jdwillmsen/gameops/minecraft/crew/internal/statusapi"
	"github.com/jdwillmsen/gameops/minecraft/crew/internal/task"
	"github.com/jdwillmsen/gameops/minecraft/crew/internal/world/blocks"
	"github.com/jdwillmsen/gameops/minecraft/crew/internal/world/shape"
	"github.com/jdwillmsen/gameops/minecraft/crew/internal/world/shape/blocktable"
	"github.com/sandertv/gophertunnel/minecraft"
	"github.com/sandertv/gophertunnel/minecraft/protocol/login"
	"github.com/sandertv/gophertunnel/minecraft/protocol/packet"
)

const (
	connectTimeout = 30 * time.Second
	// Far longer than connectTimeout: a freshly started server loads its
	// world for the first player in, and abandoning a login the server would
	// have completed is worse than waiting.
	spawnTimeout = 90 * time.Second

	// Bounds the wait for the server to be told the unit left. Without it the
	// process exits first, the server keeps the old player until its own
	// timeout, and the next login comes up as "crew-1(2)". Well inside the
	// pod's termination grace period.
	leaveTimeout = 5 * time.Second

	// The overworld's -64..319 in 16-block sections.
	sectionsPerColumn = 24
)

// storedSubChunks sizes the world store to the whole view, one chunk of margin
// all round. The server never resends a chunk the client already had, so a
// smaller store loses blocks for good. A solid section is about 16 KiB and an
// all-air one costs nothing, so a view of 8 is at most about 100 MiB.
func storedSubChunks(viewDistance uint8) int {
	r := float64(viewDistance) + 1
	return int(math.Ceil(math.Pi*r*r)) * sectionsPerColumn
}

var version = "dev"

func main() {
	log := logging.New(os.Getenv("LOG_LEVEL"))

	cfg, err := config.Load()
	if err != nil {
		log.Error("config_invalid", logging.Fields{"error": err.Error()})
		os.Exit(1)
	}

	log = log.With(logging.Fields{"unit": cfg.UnitID})

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	code := run(ctx, stop, cfg, openTokenSource, log)
	stop()
	os.Exit(code)
}

// run is the unit's whole life and returns the process exit code. stop ends
// ctx, for a failure no caller of run could otherwise signal.
func run(ctx context.Context, stop context.CancelFunc, cfg config.Config, open tokenOpener, log *logging.Logger) int {
	if cfg.Offline {
		log.Warn("auth_offline", logging.Fields{"note": "no sign-in; only a server with online mode off accepts this unit"})
	}
	// The server collides with every block's real shape, not only the families
	// shape models by hand.
	shape.Fallback = blocktable.Boxes
	tracker := belief.New(time.Now)
	dec := newDecoder(log)
	hub, beginSession := newCamera(cfg, tracker, dec, log)
	world := blocks.New(dec, storedSubChunks(cfg.ViewDistance))
	lf := life.New(lifeConfig(cfg, world), world, tracker.Snapshot, logAdapter{log}, rand.New(rand.NewSource(time.Now().UnixNano())))
	guard := skill.Guard{Each: lf.EachEntity, Threat: life.Threat, Crew: arena.DefaultCrewNames}
	runner := task.NewRunner(func(t task.Template) ([]skill.Skill, error) {
		return task.Expand(t, world, tracker.Snapshot, skill.WithLogger(logAdapter{log}), skill.WithGuard(guard))
	}, nil, tracker.Snapshot, time.Duration(cfg.TaskTimeoutS)*time.Second, logAdapter{log})
	runner.SetRole(string(cfg.Role))
	// Before anything that waits on the network: the liveness probe hits this
	// server, and a unit whose sign-in or control-plane lookup is slow would
	// otherwise be restarted before it ever answered. /readyz stays 503 until
	// the unit is in the world.
	tacticsView := newTacticsState(lf)
	srv := &http.Server{Addr: cfg.StatusAddr, Handler: statusHandler(tracker, version, unitInfo(cfg), hub, runner, cfg.TasksToken, statusapi.WithFight(lf), statusapi.WithTactics(tacticsView)), ReadHeaderTimeout: 5 * time.Second}
	var statusFailed atomic.Bool
	go func() {
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Error("status_api_failed", logging.Fields{"error": err.Error()})
			statusFailed.Store(true)
			stop()
		}
	}()
	// Bounded: Shutdown waits up to five seconds for a connection that never
	// sent a request, which would hold the whole process open on exit.
	defer func() {
		shutdownCtx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		_ = srv.Shutdown(shutdownCtx)
	}()
	// A unit that cannot serve /healthz must not exit as if it had finished,
	// and the cancel that follows can make a later step fail with a misleading
	// error, so the status failure decides the exit code.
	exit := func(code int) int {
		if statusFailed.Load() {
			return 1
		}
		return code
	}

	// SetSquad must precede the runner's first tick, and the client's
	// goroutines are the unit's own, so they end with the signal context.
	if sc := newSquadClient(cfg, tracker.Snapshot, logAdapter{log}); sc != nil {
		go sc.Run(ctx)
		lf.SetSquad(sc)
		if ps := newProfileSync(cfg, sc, lf, logAdapter{log}); ps != nil {
			ps.state = tacticsView
			go ps.run(ctx, profilePoll)
		}
	} else if cfg.Role.IsWarrior() {
		log.Warn("squad_off", logging.Fields{"note": "CREW_ROLE is set but CONTROL_URL is not; the warrior leads itself and hears no orders"})
	}
	runner.SetReflex(lf)
	go runner.Run(ctx)
	plan, err := task.ParsePlan(cfg.WorkPlan)
	if err != nil {
		log.Error("config_invalid", logging.Fields{"error": "WORK_PLAN: " + err.Error()})
		return exit(1)
	}
	if plan != nil {
		go task.NewPlanner(runner, plan).Run(ctx)
	}
	if cfg.ControlURL != nil {
		go control.New(cfg.ControlURL, cfg.UnitID, cfg.ControlToken, runner, logAdapter{log}).Run(ctx)
	}
	auth, err := resolveAuth(ctx, cfg, open)
	if err != nil {
		log.Error("auth_failed", logging.Fields{"error": err.Error()})
		return exit(1)
	}

	if cfg.JoinDelayS > 0 {
		select {
		case <-time.After(time.Duration(cfg.JoinDelayS) * time.Second):
		case <-ctx.Done():
			return exit(0)
		}
	}

	runConnectLoop(ctx,
		loopOptions{
			minDelay: time.Duration(cfg.ReconnectMinMs) * time.Millisecond,
			maxDelay: time.Duration(cfg.ReconnectMaxMs) * time.Millisecond,
			after:    time.After,
		},
		func(ctx context.Context) error {
			return session(ctx, cfg, auth, tracker, world, hub, runner, lf, beginSession, log)
		},
		tracker.Reset,
		logAdapter{log},
	)
	return exit(0)
}

// logAdapter exists because Go method sets do not unify the named
// logging.Fields with the plain map the loop's interface is written against.
type logAdapter struct{ l *logging.Logger }

func (a logAdapter) Info(e string, f map[string]any)  { a.l.Info(e, logging.Fields(f)) }
func (a logAdapter) Error(e string, f map[string]any) { a.l.Error(e, logging.Fields(f)) }

// declinePack refuses every pack the server offers. Unit login then skips the
// chunked download and goes straight to the world; the server still lets the
// unit in when it marks packs required.
func declinePack(uuid.UUID, string, int, int) bool { return false }

// spawnedFields is the spawn log line. It is a function of its own so a test
// can hold it to the rule that positions never reach a log.
func spawnedFields(xuid string, viewDistance uint8) logging.Fields {
	return logging.Fields{"xuid": xuid, "view_distance": viewDistance}
}

func session(ctx context.Context, cfg config.Config, auth authn, tracker *belief.Tracker, world *blocks.Store, hub *camera.Hub, runner *task.Runner, lf *life.Life, beginSession func(ctx context.Context, addr string), log *logging.Logger) error {
	unitSkin := unitSkinFor(cfg, log)
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

	conn, leave, err := mcdial.DialWithLeave(ctx, minecraft.Dialer{
		TokenSource:  auth.tokens,
		IdentityData: auth.identity,
		Protocol:     proto,
		// A headless unit never renders, so a world resource pack is dead
		// weight that the login would otherwise wait on.
		DownloadResourcePack: declinePack,
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
	defer leave(leaveTimeout)

	// ReadPacket does not watch ctx, so shutdown would otherwise wait for the
	// server's next packet.
	stopClose := context.AfterFunc(ctx, func() { leave(leaveTimeout) })
	defer stopClose()

	if err := conn.DoSpawnContext(ctx); err != nil {
		return fmt.Errorf("spawn: %w", err)
	}
	gd := conn.GameData()
	tracker.Spawn(gd.EntityRuntimeID, gd.PlayerPosition, gd.Yaw, gd.Pitch, gd.Dimension)
	tracker.SetUniqueID(gd.EntityUniqueID)
	tracker.ItemNames(gd.Items)
	world.Reset()
	defer world.Reset()
	u := newUnit(gd.EntityRuntimeID, tracker, world, hub, runner, lf, conn)
	u.correctionLog = logAdapter{log}
	u.items = gd.Items
	defer u.end()
	beginSession(ctx, addr)
	if hub != nil {
		hub.Spawn(gd.EntityRuntimeID)
		hub.SetItems(gd.Items)
		hub.SetClock(gd.Time, dayCycleOn(gd.GameRules), gd.DayCycleLockTime)
		// The server never sends a unit its own skin back, and the camera
		// draws the unit's body in the third-person modes.
		if px, err := base64.StdEncoding.DecodeString(unitSkin.Data); err == nil {
			hub.SetOwnSkin(unitSkin.Width, unitSkin.Height, px, false)
		}
		defer hub.Reset()
	}
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
		if err := u.apply(pk); err != nil {
			return fmt.Errorf("start acting: %w", err)
		}

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
		if respawner.Dead() {
			tracker.SetDead(true)
			u.died()
			log.Info("died", logging.Fields{"session_id": sessionid.Current()})
			continue
		}
		// Before the unit counts as alive, so a task never starts from the
		// pose it died at.
		u.respawned()
		tracker.SetDead(false)
		log.Info("respawned", logging.Fields{"session_id": sessionid.Current()})
		// The server drops the radius when the player is replaced.
		if err := conn.WritePacket(&packet.RequestChunkRadius{ChunkRadius: int32(cfg.ViewDistance), MaxChunkRadius: cfg.ViewDistance}); err != nil {
			return fmt.Errorf("request chunk radius after respawn: %w", err)
		}
	}
}

// unitSkinFor picks what the unit wears. A headless client uploads no skin of
// its own, and without one it joins as a black silhouette under a fresh skin
// ID every connect. A hand-painted file wins, then the role's outfit; a bad
// file is logged and skipped so a typo cannot keep a unit out of the world.
func unitSkinFor(cfg config.Config, log *logging.Logger) skin.Skin {
	custom, found, err := skin.Override(cfg.SkinDir, cfg.Username)
	if err != nil {
		log.Error("skin_override_invalid", logging.Fields{"error": err.Error()})
	}
	if found {
		return custom
	}
	persona := skin.Persona{Role: cfg.SkinRole}
	if cfg.Role.IsWarrior() {
		persona = skin.Persona{Role: "warrior", SquadRole: string(cfg.Role)}
	}
	return skin.ForPersona(cfg.Username, persona)
}
