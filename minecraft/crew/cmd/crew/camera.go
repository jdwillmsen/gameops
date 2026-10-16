package main

import (
	"context"
	"net/http"
	"sync"
	"time"

	"github.com/sandertv/go-raknet"

	"github.com/jdwillmsen/gameops/minecraft/agent/pkg/logging"
	"github.com/jdwillmsen/gameops/minecraft/agent/pkg/mcproto"
	"github.com/jdwillmsen/gameops/minecraft/crew/internal/atlas"
	"github.com/jdwillmsen/gameops/minecraft/crew/internal/belief"
	"github.com/jdwillmsen/gameops/minecraft/crew/internal/camera"
	"github.com/jdwillmsen/gameops/minecraft/crew/internal/config"
	"github.com/jdwillmsen/gameops/minecraft/crew/internal/statusapi"
)

// newCamera returns a nil hub unless the camera was asked for. A nil hub is
// how the rest of main knows to build no routes and do no per-packet work.
// beginSession tells the decoder which server a session runs against; it is
// always safe to call.
func newCamera(cfg config.Config, tracker *belief.Tracker, log *logging.Logger) (hub *camera.Hub, beginSession func(ctx context.Context, addr string)) {
	if !cfg.CameraEnabled {
		return nil, func(context.Context, string) {}
	}
	dec := camera.NewNetworkDecoder(func(serverVersion string) {
		log.Warn("camera_unmapped_block_hashes", logging.Fields{
			"server_version": serverVersion,
			"hint":           "the block table does not match this server; the camera draws placeholders",
		})
	})
	hub = camera.New(camera.Options{
		Decoder:    dec,
		Self:       tracker,
		MaxViewers: cfg.CameraMaxViewers,
	})
	atlases := &atlasLoader{dir: cfg.CameraAtlasDir, hub: hub, log: log, build: ensureAtlas(atlas.Fetcher{OnDownload: logAtlasDownload(log)})}
	return hub, func(ctx context.Context, addr string) {
		version := pingVersion(ctx, addr)
		dec.BeginSession(version)
		go atlases.load(context.WithoutCancel(ctx), version)
	}
}

// atlasTimeout bounds the background build: the download is a few thousand
// small files, and a stuck one must not hold the loader forever.
const atlasTimeout = 20 * time.Minute

func logAtlasDownload(log *logging.Logger) func(atlas.Pack) {
	return func(p atlas.Pack) {
		log.Info("camera_atlas_downloaded", logging.Fields{
			"tag":         p.Tag,
			"bytes":       p.Bytes,
			"files":       p.Files,
			"requests":    p.Requests,
			"duration_ms": p.Elapsed.Milliseconds(),
		})
	}
}

type atlasBuilder func(ctx context.Context, serverVersion, dir string) (*atlas.Atlas, error)

func ensureAtlas(f atlas.Fetcher) atlasBuilder {
	return func(ctx context.Context, serverVersion, dir string) (*atlas.Atlas, error) {
		a, _, err := atlas.Ensure(ctx, f, serverVersion, dir, atlas.Options{})
		return a, err
	}
}

// atlasLoader builds the texture atlas once the server version is known. A
// failure only costs the textures: the camera keeps drawing flat colours and
// the next session tries again.
type atlasLoader struct {
	dir   string
	hub   *camera.Hub
	log   *logging.Logger
	build atlasBuilder

	mu            sync.Mutex
	running, done bool
}

func (l *atlasLoader) load(ctx context.Context, serverVersion string) {
	l.mu.Lock()
	if serverVersion == "" || l.running || l.done {
		l.mu.Unlock()
		return
	}
	l.running = true
	l.mu.Unlock()

	ctx, cancel := context.WithTimeout(ctx, atlasTimeout)
	defer cancel()
	a, err := l.build(ctx, serverVersion, l.dir)
	if err == nil {
		err = l.hub.SetAtlas(a)
	}
	l.mu.Lock()
	l.running, l.done = false, err == nil
	l.mu.Unlock()
	if err != nil {
		l.log.Warn("camera_atlas_unavailable", logging.Fields{
			"server_version": serverVersion,
			"error":          err.Error(),
			"hint":           "the camera draws flat colours until an atlas is built",
		})
		return
	}
	l.log.Info("camera_atlas_ready", logging.Fields{"server_version": serverVersion})
}

// pingVersion asks the server for the version it advertises. The game data's
// own version field is a wildcard on current servers, so the ping is the only
// place the real one is.
func pingVersion(ctx context.Context, addr string) string {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	pong, err := raknet.PingContext(ctx, addr)
	if err != nil {
		return ""
	}
	ad, err := mcproto.ParsePong(string(pong))
	if err != nil {
		return ""
	}
	return ad.Version
}

func statusHandler(tracker *belief.Tracker, version string, hub *camera.Hub) http.Handler {
	h := statusapi.Handler(tracker, version)
	if hub == nil {
		return h
	}
	return camera.Mount(h, hub)
}
