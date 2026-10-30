package main

import (
	"context"
	"math/rand/v2"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/sandertv/gophertunnel/minecraft/protocol"

	"github.com/jdwillmsen/gameops/minecraft/agent/pkg/logging"
	"github.com/jdwillmsen/gameops/minecraft/agent/pkg/mcproto"
	"github.com/jdwillmsen/gameops/minecraft/crew/internal/atlas"
	"github.com/jdwillmsen/gameops/minecraft/crew/internal/belief"
	"github.com/jdwillmsen/gameops/minecraft/crew/internal/camera"
	"github.com/jdwillmsen/gameops/minecraft/crew/internal/config"
	"github.com/jdwillmsen/gameops/minecraft/crew/internal/statusapi"
	"github.com/jdwillmsen/gameops/minecraft/crew/internal/task"
)

// newDecoder is the block table both the world store and the camera read, so
// it exists whether or not the camera is on.
func newDecoder(log *logging.Logger) *camera.NetworkDecoder {
	return camera.NewNetworkDecoder(func(serverVersion string) {
		log.Warn("camera_unmapped_block_hashes", logging.Fields{
			"server_version": serverVersion,
			"hint":           "the block table does not match this server; unknown blocks are drawn as placeholders and treated as solid",
		})
	})
}

// newCamera returns a nil hub unless the camera was asked for. A nil hub is
// how the rest of main knows to build no routes and do no per-packet work.
// beginSession tells the decoder which server a session runs against; it is
// always safe to call.
func newCamera(cfg config.Config, tracker *belief.Tracker, dec *camera.NetworkDecoder, log *logging.Logger) (hub *camera.Hub, beginSession func(ctx context.Context, addr string)) {
	if !cfg.CameraEnabled {
		return nil, func(ctx context.Context, addr string) { dec.BeginSession(pingVersion(ctx, addr)) }
	}
	hub = camera.New(camera.Options{
		Decoder:    dec,
		Self:       tracker,
		MaxViewers: cfg.CameraMaxViewers,
	})
	atlases := &atlasLoader{dir: cfg.CameraAtlasDir, hub: hub, log: log}
	atlases.build = ensureAtlas(atlas.Fetcher{OnDownload: logAtlasDownload(log)}, func(err error) {
		atlases.fellBack.Store(true)
		log.Warn("camera_atlas_rebuild_failed", logging.Fields{
			"error": err.Error(),
			"hint":  "serving the older cached atlas, which lacks the item sheet, the mob models or the armour",
		})
	})
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

func ensureAtlas(f atlas.Fetcher, onFallback func(error)) atlasBuilder {
	return func(ctx context.Context, serverVersion, dir string) (*atlas.Atlas, error) {
		a, _, err := atlas.Ensure(ctx, f, serverVersion, dir, atlas.Options{OnFallback: onFallback})
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

	// retry is how long to wait before building again after a failure or an
	// incomplete atlas; zero means the default. It doubles with each failure
	// in a row and is jittered, so that twenty units restarted together do not
	// ask the pack host in the same minute.
	retry time.Duration
	// fellBack is set by the build when it could only serve an older cached
	// atlas, which is worth another attempt later.
	fellBack atomic.Bool

	mu            sync.Mutex
	running, done bool
	// version is the latest server version seen, so a pending retry builds
	// for the server the unit talks to now.
	version  string
	failures int
	pending  *time.Timer
}

const (
	defaultAtlasRetry = 5 * time.Minute
	maxAtlasRetry     = time.Hour
)

// retryDelay is drawn from [d, 2d) where d doubles per failure, and d stops
// at half the cap so the jittered delay never exceeds it.
func (l *atlasLoader) retryDelay(failures int) time.Duration {
	d := l.retry
	if d == 0 {
		d = defaultAtlasRetry
	}
	for i := 1; i < failures && d < maxAtlasRetry/2; i++ {
		d *= 2
	}
	d = min(d, maxAtlasRetry/2)
	return d + rand.N(d)
}

func (l *atlasLoader) load(ctx context.Context, serverVersion string) {
	l.mu.Lock()
	if serverVersion != "" {
		l.version = serverVersion
	}
	serverVersion = l.version
	if serverVersion == "" || l.running || l.done {
		l.mu.Unlock()
		return
	}
	if l.pending != nil {
		l.pending.Stop()
		l.pending = nil
	}
	l.running = true
	l.mu.Unlock()
	l.fellBack.Store(false)

	bctx, cancel := context.WithTimeout(ctx, atlasTimeout)
	a, err := l.build(bctx, serverVersion, l.dir)
	cancel()
	if err == nil {
		err = l.hub.SetAtlas(a)
	}
	complete := err == nil && !l.fellBack.Swap(false)
	l.mu.Lock()
	l.running, l.done = false, complete
	if complete {
		l.failures = 0
	} else {
		// The pack host rate-limits by address and every unit shares one, so a
		// start that finds the limit spent keeps trying instead of waiting for
		// the next reconnect.
		l.failures++
		l.pending = time.AfterFunc(l.retryDelay(l.failures), func() { l.load(ctx, "") })
	}
	l.mu.Unlock()
	if err != nil {
		l.log.Warn("camera_atlas_unavailable", logging.Fields{
			"server_version": serverVersion,
			"error":          err.Error(),
			"hint":           "the camera draws flat colours until an atlas is built",
		})
		return
	}
	if ierr := a.ItemsErr(); ierr != nil {
		l.log.Warn("camera_items_unavailable", logging.Fields{
			"server_version": serverVersion,
			"error":          ierr.Error(),
			"hint":           "the overlay shows item names instead of pictures",
		})
	}
	l.log.Info("camera_atlas_ready", logging.Fields{"server_version": serverVersion})
}

// pingVersion asks the server for the version it advertises, over whichever
// transport addr names. The game data's own version field is a wildcard on
// current servers, so the ping (or a NetherNet server's status) is the only
// place the real one is.
func pingVersion(ctx context.Context, addr string) string {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	ad, err := mcproto.Advertise(ctx, addr)
	if err != nil {
		return ""
	}
	return ad.Version
}

// statusHandler mounts the task routes only when a token is set, so a unit
// without one has no way to be told what to do.
func statusHandler(tracker *belief.Tracker, version string, unit statusapi.Unit, hub *camera.Hub, runner *task.Runner, tasksToken string, opts ...statusapi.Option) http.Handler {
	mux := http.NewServeMux()
	var tasks statusapi.TaskSource
	if runner != nil {
		tasks = runner
	}
	mux.Handle("/", statusapi.Handler(tracker, version, unit, tasks, opts...))
	task.Routes(mux, runner, tasksToken)
	if hub == nil {
		return mux
	}
	return camera.Mount(mux, hub)
}

// dayCycleOn reports the doDaylightCycle rule; the server sends it unless the
// rule is at its default, so a missing rule means the day runs.
func dayCycleOn(rules []protocol.GameRule) bool {
	for _, r := range rules {
		if strings.EqualFold(r.Name, "dodaylightcycle") {
			if on, ok := r.Value.(bool); ok {
				return on
			}
		}
	}
	return true
}
