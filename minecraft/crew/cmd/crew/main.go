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
	"github.com/jdwillmsen/gameops/minecraft/crew/internal/camera"
	"github.com/jdwillmsen/gameops/minecraft/crew/internal/config"
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
	hub, beginSession := newCamera(cfg, tracker, log)
	srv := &http.Server{Addr: cfg.StatusAddr, Handler: statusHandler(tracker, version, hub), ReadHeaderTimeout: 5 * time.Second}
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
		func(ctx context.Context) error { return session(ctx, cfg, ts, tracker, hub, beginSession, log) },
		tracker.Reset,
		logAdapter{log},
	)
}

// logAdapter exists because Go method sets do not unify the named
// logging.Fields with the plain map the loop's interface is written against.
type logAdapter struct{ l *logging.Logger }

func (a logAdapter) Info(e string, f map[string]any)  { a.l.Info(e, logging.Fields(f)) }
func (a logAdapter) Error(e string, f map[string]any) { a.l.Error(e, logging.Fields(f)) }

// spawnedFields is the spawn log line. It is a function of its own so a test
// can hold it to the rule that positions never reach a log.
func spawnedFields(xuid string, viewDistance uint8) logging.Fields {
	return logging.Fields{"xuid": xuid, "view_distance": viewDistance}
}

func session(ctx context.Context, cfg config.Config, ts oauth2.TokenSource, tracker *belief.Tracker, hub *camera.Hub, beginSession func(ctx context.Context, addr string), log *logging.Logger) error {
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
	tracker.Spawn(gd.EntityRuntimeID, gd.PlayerPosition, gd.Yaw, gd.Pitch, gd.Dimension)
	beginSession(ctx, addr)
	if hub != nil {
		hub.Spawn(gd.EntityRuntimeID)
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
		tracker.Apply(pk)
		// After belief: the hub reads the unit's pose back from it.
		if hub != nil {
			hub.Apply(pk, conn)
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
