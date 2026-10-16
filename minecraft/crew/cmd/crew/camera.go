package main

import (
	"context"
	"net/http"
	"time"

	"github.com/sandertv/go-raknet"

	"github.com/jdwillmsen/gameops/minecraft/agent/pkg/logging"
	"github.com/jdwillmsen/gameops/minecraft/agent/pkg/mcproto"
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
	return hub, func(ctx context.Context, addr string) { dec.BeginSession(pingVersion(ctx, addr)) }
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
