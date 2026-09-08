// Command bot holds a player slot on a Bedrock server so the chunks around it
// stay loaded.
//
// That is the whole job. It does not answer chat, run commands or persist
// anything: minecraft-server-agent does those, and this program was
// deliberately stripped back to presence on 2026-09-08 so the two do not
// overlap.
//
// Rewritten from TypeScript in Go for two reasons. The measured one: the Node
// implementation used roughly eight times the CPU and memory of the Go agent
// while doing considerably less -- 48-56m CPU and 92-97Mi against 6m and 12Mi.
// The structural one: respawn-on-death, device-code auth, structured logging
// and a generated skin already exist in the agent's pkg/, and writing them a
// second time in another language guarantees the second copy rots.
package main

import (
	"context"
	"fmt"
	"math/rand"
	"net"
	"os"
	"os/signal"
	"strconv"
	"syscall"
	"time"

	"github.com/jdwillmsen/minecraft-afk-bot/internal/config"
	"github.com/jdwillmsen/minecraft-afk-bot/internal/liveness"
	"github.com/jdwillmsen/minecraft-afk-bot/internal/logging"
	"github.com/jdwillmsen/minecraft-afk-bot/internal/mcauth"
	"github.com/jdwillmsen/minecraft-afk-bot/internal/skin"
	"github.com/sandertv/gophertunnel/minecraft"
	"github.com/sandertv/gophertunnel/minecraft/protocol/login"
	"github.com/sandertv/gophertunnel/minecraft/protocol/packet"
	"golang.org/x/oauth2"
)

// A session lasting at least this long is treated as healthy and resets the
// reconnect backoff. Without it, a bot that connects and is immediately kicked
// retries as eagerly as one recovering from a momentary blip.
const stableSession = 60 * time.Second

func main() {
	log := logging.New(os.Getenv("LOG_LEVEL"))

	cfg, err := config.Load()
	if err != nil {
		log.Error("config_invalid", logging.Fields{"error": err.Error()})
		os.Exit(1)
	}

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	// The device code is printed by design: the first start against a fresh
	// cache genuinely needs a human, and this is the only place it appears.
	ts, err := mcauth.TokenSource(ctx, cfg.AuthCacheDir, cfg.Username, os.Stdout)
	if err != nil {
		log.Error("auth_failed", logging.Fields{"error": err.Error()})
		os.Exit(1)
	}

	runConnectLoop(ctx, cfg, ts, log)
}

// runConnectLoop keeps the bot connected for the life of the process.
//
// It reconnects rather than exiting, because exiting moves the retry loop into
// Kubernetes: a coarser backoff, a fresh pod, and a token reload on every
// attempt.
func runConnectLoop(ctx context.Context, cfg config.Config, ts oauth2.TokenSource, log *logging.Logger) {
	minDelay := time.Duration(cfg.ReconnectMinMs) * time.Millisecond
	maxDelay := time.Duration(cfg.ReconnectMaxMs) * time.Millisecond
	delay := minDelay
	first := true

	for {
		if ctx.Err() != nil {
			return
		}
		if !first {
			wait := jitter(delay)
			log.Info("reconnecting", logging.Fields{"in_ms": wait.Milliseconds()})
			select {
			case <-time.After(wait):
			case <-ctx.Done():
				return
			}
		}
		first = false

		started := time.Now()
		err := session(ctx, cfg, ts, log)
		lasted := time.Since(started)
		if ctx.Err() != nil {
			return
		}
		if err != nil {
			log.Error("session_error", logging.Fields{"error": err.Error(), "session_lasted_ms": lasted.Milliseconds()})
		} else {
			log.Info("session_ended", logging.Fields{"session_lasted_ms": lasted.Milliseconds()})
		}
		delay = backoff(delay, lasted, minDelay, maxDelay)
	}
}

// session runs one connection until it drops.
func session(ctx context.Context, cfg config.Config, ts oauth2.TokenSource, log *logging.Logger) error {
	// Without this the bot joins as a black silhouette under a SkinID
	// regenerated every connect: Bedrock skins are uploaded by the client from
	// its own installation, and a headless client has none.
	botSkin := skin.For(cfg.Username)
	dialer := minecraft.Dialer{
		TokenSource: ts,
		ClientData: login.ClientData{
			SkinID:          botSkin.ID,
			SkinData:        botSkin.Data,
			SkinImageWidth:  botSkin.Width,
			SkinImageHeight: botSkin.Height,
		},
	}

	dialCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	conn, err := dialer.DialContext(dialCtx, "raknet", net.JoinHostPort(cfg.Host, strconv.Itoa(cfg.Port)))
	cancel()
	if err != nil {
		return fmt.Errorf("dial: %w", err)
	}
	defer func() { _ = conn.Close() }()

	if err := conn.DoSpawnContext(ctx); err != nil {
		return fmt.Errorf("spawn: %w", err)
	}
	log.Info("spawned", logging.Fields{
		"xuid":          conn.IdentityData().XUID,
		"view_distance": cfg.ViewDistance,
	})

	// The reason this program exists. Without a chunk radius request the
	// server has no view distance to honour for this client, and a bot that
	// loads nothing is indistinguishable from a healthy one until someone
	// notices the farm has stopped.
	if err := conn.WritePacket(&packet.RequestChunkRadius{
		ChunkRadius:    cfg.ViewDistance,
		MaxChunkRadius: uint8(cfg.ViewDistance),
	}); err != nil {
		return fmt.Errorf("request chunk radius: %w", err)
	}

	respawner := liveness.New(conn.GameData().EntityRuntimeID)

	for {
		if ctx.Err() != nil {
			return nil
		}
		pk, err := conn.ReadPacket()
		if err != nil {
			return fmt.Errorf("read: %w", err)
		}

		// A dead bot holds no chunks, and nothing outside this loop can tell
		// it is dead: the session stays open and the pod stays ready. Measured
		// on the agent on 2026-09-08 before this was handled.
		if handled, err := respawner.Handle(pk, conn); err != nil {
			log.Error("respawn_failed", logging.Fields{"error": err.Error()})
		} else if handled {
			if respawner.Dead() {
				log.Info("died", logging.Fields{"requesting_respawn": true})
			} else {
				log.Info("respawned", nil)
				// The radius is per-session state the server drops when the
				// player is replaced, so it is asked for again rather than
				// assumed to have survived.
				if err := conn.WritePacket(&packet.RequestChunkRadius{
					ChunkRadius:    cfg.ViewDistance,
					MaxChunkRadius: uint8(cfg.ViewDistance),
				}); err != nil {
					return fmt.Errorf("request chunk radius after respawn: %w", err)
				}
			}
		}

		if radius, ok := pk.(*packet.ChunkRadiusUpdated); ok {
			// Logged because it is the server's answer to the request above,
			// and the only evidence the bot is loading what it asked for.
			log.Info("chunk_radius_granted", logging.Fields{"radius": radius.ChunkRadius})
		}
	}
}

// backoff doubles up to a ceiling, resetting when a session proved stable.
func backoff(current, lasted, min, max time.Duration) time.Duration {
	if lasted >= stableSession {
		return min
	}
	if next := current * 2; next < max {
		return next
	}
	return max
}

// jitter spreads reconnects so a server restart does not have every bot
// returning in lockstep, which is how a recovering server is hit hardest at
// the moment it can least take it.
func jitter(d time.Duration) time.Duration {
	if d <= 0 {
		return 0
	}
	return d/2 + time.Duration(rand.Int63n(int64(d/2)+1))
}
