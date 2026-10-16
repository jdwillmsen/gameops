package main

import (
	"net/http"

	"github.com/jdwillmsen/gameops/minecraft/crew/internal/belief"
	"github.com/jdwillmsen/gameops/minecraft/crew/internal/camera"
	"github.com/jdwillmsen/gameops/minecraft/crew/internal/config"
	"github.com/jdwillmsen/gameops/minecraft/crew/internal/statusapi"
)

// newCamera returns nil unless the camera was asked for. A nil hub is how the
// rest of main knows to build no routes and do no per-packet work.
func newCamera(cfg config.Config, tracker *belief.Tracker) *camera.Hub {
	if !cfg.CameraEnabled {
		return nil
	}
	return camera.New(camera.Options{
		Decoder:    camera.NewDragonflyDecoder(),
		Self:       tracker,
		MaxViewers: cfg.CameraMaxViewers,
	})
}

func statusHandler(tracker *belief.Tracker, version string, hub *camera.Hub) http.Handler {
	h := statusapi.Handler(tracker, version)
	if hub == nil {
		return h
	}
	return camera.Mount(h, hub)
}
