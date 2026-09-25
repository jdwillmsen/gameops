// Command bot holds a player slot on a Bedrock server so the chunks around it
// stay loaded.
//
// That is the whole job. It does not answer chat, run commands or persist
// anything: minecraft-server-agent does those, and keeping this program to
// presence alone is what stops the two overlapping (docs/decisions.md).
package main

import (
	"context"
	"fmt"
	"math/rand"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/signal"
	"strconv"
	"syscall"
	"time"

	"github.com/jdwillmsen/minecraft-afk-bot/internal/config"
	"github.com/jdwillmsen/minecraft-afk-bot/internal/liveness"
	"github.com/jdwillmsen/minecraft-afk-bot/internal/logging"
	"github.com/jdwillmsen/minecraft-afk-bot/internal/mcauth"
	"github.com/jdwillmsen/minecraft-afk-bot/internal/mcproto"
	"github.com/jdwillmsen/minecraft-afk-bot/internal/presence"
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

// Bounds one poll of the agent, so a hung agent delays the next answer
// rather than stopping the poller.
const presenceTimeout = 5 * time.Second

// version is what the bot reports to the agent. The image build stamps the
// release into it; a plain go build reports "dev".
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

	// The device code is printed by design: the first start against a fresh
	// cache genuinely needs a human, and this is the only place it appears.
	ts, err := mcauth.TokenSource(ctx, cfg.AuthCacheDir, cfg.Username, os.Stdout)
	if err != nil {
		log.Error("auth_failed", logging.Fields{"error": err.Error()})
		os.Exit(1)
	}

	gate, runPresence := presenceGate(cfg, log)
	if runPresence != nil {
		go runPresence(ctx)
	}

	runConnectLoop(ctx, cfg, gate, func(ctx context.Context, spawned func()) error {
		return session(ctx, cfg, ts, log, spawned)
	}, log)
}

// presenceGate returns what the connect loop waits on, and the poller to run
// beside it when PRESENCE_URL is set.
func presenceGate(cfg config.Config, log presence.Logger) (presence.Gate, func(context.Context)) {
	p := cfg.Presence
	if !p.Enabled() {
		return presence.AlwaysPresent{}, nil
	}
	client := presence.NewClient(p.URL, p.ActorID, p.Token, &http.Client{Timeout: presenceTimeout})
	r := presence.NewReconciler(client, p.Default, time.Duration(p.PollMs)*time.Millisecond, version, log)
	log.Info("presence_enabled", logging.Fields{
		"actor_id": p.ActorID,
		"url":      redactedURL(p.URL),
		"default":  string(p.Default),
		"poll_ms":  p.PollMs,
	})
	return r, r.Run
}

// redactedURL hides a URL's userinfo password before it reaches the log.
// config.Load already proved p.URL parses, so the fallback here is normally
// unreached; it exists only so a caller that skips Load (a test, or a future
// caller) still cannot leak a password through this line.
func redactedURL(raw string) string {
	if u, err := url.Parse(raw); err == nil {
		return u.Redacted()
	}
	return raw
}

// connectFunc runs one session until it ends, calling spawned once the bot
// is in the world.
type connectFunc func(ctx context.Context, spawned func()) error

// runConnectLoop keeps the bot connected for the life of the process, except
// while the gate holds it out.
//
// It reconnects rather than exiting, because exiting moves the retry loop into
// Kubernetes: a coarser backoff, a fresh pod, and a token reload on every
// attempt.
func runConnectLoop(ctx context.Context, cfg config.Config, gate presence.Gate, connect connectFunc, log *logging.Logger) {
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

		sess, release, err := gate.Admit(ctx)
		if err != nil {
			return
		}
		started := time.Now()
		err = connect(sess, func() { gate.Connected(true) })
		lasted := time.Since(started)
		gate.Connected(false)
		parked := sess.Err() != nil
		release()
		if ctx.Err() != nil {
			return
		}
		if parked {
			// A deliberate disconnect is not a failure, so it neither grows
			// the backoff nor delays the return once the gate admits again.
			log.Info("session_parked", logging.Fields{"session_lasted_ms": lasted.Milliseconds()})
			delay = minDelay
			first = true
			continue
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
func session(ctx context.Context, cfg config.Config, ts oauth2.TokenSource, log *logging.Logger, spawned func()) error {
	// Without this the bot joins as a black silhouette under a SkinID
	// regenerated every connect: Bedrock skins are uploaded by the client from
	// its own installation, and a headless client has none.
	botSkin := skin.For(cfg.Username)
	addr := net.JoinHostPort(cfg.Host, strconv.Itoa(cfg.Port))

	// The server upgrades itself to Mojang's latest on restart, and a point
	// release that only bumps the protocol number still gets this client
	// kicked before login -- which is how a whole fleet of bots goes offline
	// over a release that changed no packets. One ping per session buys the
	// server's own number to announce.
	proto := mcproto.Negotiate(ctx, addr, func(ad mcproto.Advertisement) {
		log.Warn("protocol_spoofed", logging.Fields{
			"compiled_protocol":   minecraft.DefaultProtocol.ID(),
			"advertised_protocol": ad.Protocol,
			"server_version":      ad.Version,
		})
	})

	dialer := minecraft.Dialer{
		TokenSource: ts,
		Protocol:    proto,
		ClientData: login.ClientData{
			SkinID:          botSkin.ID,
			SkinData:        botSkin.Data,
			SkinImageWidth:  botSkin.Width,
			SkinImageHeight: botSkin.Height,
		},
	}

	dialCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	conn, err := dialer.DialContext(dialCtx, "raknet", addr)
	cancel()
	if err != nil {
		return fmt.Errorf("dial: %w", err)
	}
	defer func() { _ = conn.Close() }()

	// ReadPacket does not watch ctx, so without this a park would take effect
	// only when the server next sent something, and the player would stay
	// listed until then.
	stop := context.AfterFunc(ctx, func() { _ = conn.Close() })
	defer stop()

	if err := conn.DoSpawnContext(ctx); err != nil {
		return fmt.Errorf("spawn: %w", err)
	}
	log.Info("spawned", logging.Fields{
		"xuid":          conn.IdentityData().XUID,
		"view_distance": cfg.ViewDistance,
	})
	spawned()

	// The reason this program exists. Without a chunk radius request the
	// server has no view distance to honour for this client, and a bot that
	// loads nothing is indistinguishable from a healthy one until someone
	// notices the farm has stopped.
	if err := conn.WritePacket(&packet.RequestChunkRadius{
		ChunkRadius:    int32(cfg.ViewDistance),
		MaxChunkRadius: cfg.ViewDistance,
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
			if ctx.Err() != nil {
				return nil
			}
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
					ChunkRadius:    int32(cfg.ViewDistance),
					MaxChunkRadius: cfg.ViewDistance,
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
