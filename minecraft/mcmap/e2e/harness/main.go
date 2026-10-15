// Command harness serves the map over a world that is nobody's, for the
// browser checks to be run against where there is no cluster: on a
// developer's machine and in CI. It is the real server, both listeners,
// with the login on and service sessions as the environment sets them; only
// what the server would have read from a game is made up here.
//
// Nothing in it is a real world's: no coordinate, name or seed. It fetches
// nothing, so it runs with no network.
package main

import (
	"bytes"
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/json"
	"errors"
	"image"
	"image/color"
	"image/png"
	"io/fs"
	"log/slog"
	"math"
	"math/big"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/signal"
	"path/filepath"
	"sort"
	"strconv"
	"syscall"
	"time"

	"github.com/jdwillmsen/gameops/minecraft/mcmap/internal/auth"
	"github.com/jdwillmsen/gameops/minecraft/mcmap/internal/biomes"
	"github.com/jdwillmsen/gameops/minecraft/mcmap/internal/chunks"
	"github.com/jdwillmsen/gameops/minecraft/mcmap/internal/config"
	"github.com/jdwillmsen/gameops/minecraft/mcmap/internal/heights"
	"github.com/jdwillmsen/gameops/minecraft/mcmap/internal/history"
	"github.com/jdwillmsen/gameops/minecraft/mcmap/internal/icons"
	"github.com/jdwillmsen/gameops/minecraft/mcmap/internal/leveldat"
	"github.com/jdwillmsen/gameops/minecraft/mcmap/internal/live"
	"github.com/jdwillmsen/gameops/minecraft/mcmap/internal/markers"
	"github.com/jdwillmsen/gameops/minecraft/mcmap/internal/operators"
	"github.com/jdwillmsen/gameops/minecraft/mcmap/internal/players"
	"github.com/jdwillmsen/gameops/minecraft/mcmap/internal/render"
	"github.com/jdwillmsen/gameops/minecraft/mcmap/internal/server"
	"github.com/jdwillmsen/gameops/minecraft/mcmap/internal/structures"
	"github.com/jdwillmsen/gameops/minecraft/mcmap/internal/trails"
	"github.com/jdwillmsen/gameops/minecraft/mcmap/internal/worker"
	"github.com/jdwillmsen/gameops/minecraft/mcmap/web"
)

// A name tag is a player's text. One that would be markup if it were ever
// written as markup is in the world so that the checks would see it run.
const hostile = "<img src=x onerror=alert(1)>"

func main() {
	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil))
	if err := run(logger); err != nil {
		logger.Error("exiting", "error", err)
		os.Exit(1)
	}
}

func run(logger *slog.Logger) error {
	dir, err := os.MkdirTemp("", "mcmap-harness-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(dir)
	// The service's own reading of its environment, so that a secret the
	// service would refuse is refused here too. What it needs and the
	// harness has no use for is given a stand-in.
	cfg, err := config.Load(func(key string) string {
		if v := os.Getenv(key); v != "" {
			return v
		}
		return map[string]string{
			"BRIDGE_URL":     "http://bridge.invalid",
			"BRIDGE_TOKEN":   "harness-has-no-bridge",
			"LEVEL_NAME":     "Harness",
			"INTERNAL_TOKEN": "harness-internal-token-not-a-secret",
			"HTTP_ADDR":      "127.0.0.1:18080",
			"INTERNAL_ADDR":  "127.0.0.1:19090",
			// Whatever is kept is thrown away with the rest.
			"DATA_DIR": dir,
		}[key]
	})
	if err != nil {
		return err
	}
	quiet := os.Getenv("HARNESS_QUIET") == "1"

	maps := filepath.Join(dir, "maps")
	renderer := &render.Unmined{}
	if err := terrain(maps, renderer); err != nil {
		return err
	}
	now := time.Now()
	status := &worker.Status{}
	// The end is left unrendered, as a dimension nobody has been to is.
	status.MarkRendered("overworld", now)
	status.MarkRendered("nether", now)
	status.MarkRendered(harnessLevel.Dir(), now)

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	marked := markers.NewStore()
	marked.Set(now, markedWorld())

	// Where the map is to keep its days, it keeps this one: the terrain
	// just written and the markers just read, by the capture the service
	// makes at the end of a cycle.
	var past *history.Store
	if cfg.History {
		if past, err = history.Open(cfg.HistoryDir, logger); err != nil {
			return err
		}
		past.MapsDir, past.Source, past.Markers = maps, cycle{renderer}, marked.Scanned
		if _, err := past.Capture(ctx, now); err != nil {
			return err
		}
	}

	layer := live.New(10*time.Second, 1000, logger)
	biomeStore := &biomes.Store{}
	biomeStore.Set(biomeWorld(now))
	layer.Surface = biomeStore.Surface
	recorder := trails.New(24*time.Hour, 5000)
	saved := &players.Store{}
	saved.Set(now, map[int64]players.Record{1: harnessPlayer()})

	mobs := &icons.Mobs{Dir: filepath.Join(dir, "icons"), Ref: "harness", Logger: logger,
		Fetch: func(context.Context) (icons.Set, error) { return art() }}
	mobs.Run(ctx)
	heads := &icons.Heads{TTL: 24 * time.Hour}
	if !quiet {
		steveHead, err := square(8, color.NRGBA{255, 0, 255, 255})
		if err != nil {
			return err
		}
		alexHead, err := square(16, color.NRGBA{0, 255, 255, 255})
		if err != nil {
			return err
		}
		if _, err := heads.Replace([]icons.Report{
			{XUID: "1001", Gamertag: "HarnessPlayerOne", Head: steveHead},
			{XUID: "1009", Gamertag: "HarnessPlayerTwo", Head: alexHead},
		}); err != nil {
			return err
		}
	}

	app := &server.Server{
		Renderer:      renderer,
		NetherTop:     100,
		NetherLevels:  []render.NetherLevel{harnessLevel},
		MapsDir:       maps,
		World:         cfg.Level,
		Status:        status,
		Refresh:       time.Hour,
		Static:        web.FS,
		Log:           logger,
		InternalToken: cfg.InternalToken,
		Live:          layer,
		LiveKeepalive: cfg.LiveKeepalive,
		Markers:       marked,
		// So that the route a service session must be refused is there
		// to refuse it.
		Waypoints:  waypoints{},
		MobIcons:   mobs,
		Art:        faced{mobs},
		Heads:      heads,
		Structures: surveyed{survey(now)},
		Biomes:     biomeStore,
		Trails:     recorder,
		Players:    saved,
	}
	if past != nil {
		app.History = past
	}
	if dir := os.Getenv("HARNESS_WEB"); dir != "" {
		// Another build of the page over the same world: how a check is
		// shown to fail on the page a fault was in.
		app.Static = os.DirFS(dir)
	}
	key := make([]byte, 32)
	for i := range key {
		key[i] = byte(i)
	}
	app.Sessions = &auth.Sessions{Key: key, TTL: cfg.SessionTTL, Now: time.Now}
	app.Codes = &auth.Codes{TTL: 10 * time.Minute, Max: 100, Now: time.Now}
	if cfg.ServiceSecret != "" {
		app.Sessions.ServiceKey = auth.ServiceKey(key, cfg.ServiceSecret)
		app.Sessions.ServiceTTL = cfg.ServiceSessionTTL
		app.Sessions.ServiceSeen = server.CountServiceRequest
		app.Service = auth.NewExchange(cfg.ServiceSecret, time.Now)
	}
	// A bridge that answers GET /permissions and nothing else, with one
	// made-up operator, so that the operator list runs as it does in
	// production and every session the suite holds is refused by the admin
	// routes. The suite never completes a ceremony, so the passkey origin
	// only has to parse.
	permissions := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/permissions" || r.Header.Get("Authorization") != "Bearer "+cfg.BridgeToken {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"2535412345678909":"operator"}`))
	}))
	defer permissions.Close()
	list, err := operators.Open(filepath.Join(dir, "auth", "operators.json"), operators.NewBridge(permissions.URL, cfg.BridgeToken), time.Now, logger)
	if err != nil {
		return err
	}
	list.Refresh(ctx)
	app.Admin, app.Operators = true, list
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

	go play(ctx, layer, recorder, quiet)

	public := &http.Server{Handler: app.Handler(), ReadHeaderTimeout: 5 * time.Second}
	internal := &http.Server{Handler: app.InternalHandler(), ReadHeaderTimeout: 5 * time.Second}
	publicListener, err := net.Listen("tcp", cfg.HTTPAddr)
	if err != nil {
		return err
	}
	internalListener, err := net.Listen("tcp", cfg.InternalAddr)
	if err != nil {
		return err
	}
	go func() {
		<-ctx.Done()
		layer.Hub.Close()
		shutdown, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		_ = public.Shutdown(shutdown)
		_ = internal.Shutdown(shutdown)
	}()
	// Said once both are bound: whoever started this waits for the line.
	logger.Info("harness listening", "http_addr", publicListener.Addr().String(), "internal_addr", internalListener.Addr().String(), "service_sessions", cfg.ServiceSecret != "", "history", cfg.History, "quiet", quiet)
	// The page is served over TLS, as the gateway serves it: the session
	// cookie is Secure and bound to its host by name, and a browser will
	// not be given one for a plain address. The certificate is made here
	// and trusted by nobody, which a check has to be told to accept.
	certificate, err := selfSigned()
	if err != nil {
		return err
	}
	public.TLSConfig = &tls.Config{Certificates: []tls.Certificate{certificate}, MinVersion: tls.VersionTLS12}
	// HTTP/1.1 only, which is what the gateway speaks to the service. Over
	// HTTP/2 a write deadline ends a stream when it passes, written to or
	// not, and a quiet live stream would be cut here in a way it never is
	// where the map runs.
	public.TLSNextProto = map[string]func(*http.Server, *tls.Conn, http.Handler){}
	errs := make(chan error, 2)
	go func() { errs <- internal.Serve(internalListener) }()
	go func() { errs <- public.ServeTLS(publicListener, "", "") }()
	if err := <-errs; err != nil && !errors.Is(err, http.ErrServerClosed) {
		return err
	}
	return nil
}

func selfSigned() (tls.Certificate, error) {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return tls.Certificate{}, err
	}
	template := &x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject:      pkix.Name{CommonName: "mcmap harness"},
		NotBefore:    time.Now().Add(-time.Minute),
		NotAfter:     time.Now().Add(24 * time.Hour),
		KeyUsage:     x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		DNSNames:     []string{"localhost"},
		IPAddresses:  []net.IP{net.IPv4(127, 0, 0, 1), net.IPv6loopback},
	}
	der, err := x509.CreateCertificate(rand.Reader, template, template, &key.PublicKey, key)
	if err != nil {
		return tls.Certificate{}, err
	}
	return tls.Certificate{Certificate: [][]byte{der}, PrivateKey: key}, nil
}

// cycle is the renderer as a day is kept from it. A day is kept only of a
// cycle that drew all three dimensions, and the harness leaves the end
// undrawn so that the page has one such to show; for the day it is
// answered for here, as a dimension drawn with nothing in it.
type cycle struct{ *render.Unmined }

func (c cycle) Info(out string) (render.Info, error) {
	info, err := c.Unmined.Info(out)
	if errors.Is(err, fs.ErrNotExist) {
		return render.Info{Format: "png"}, nil
	}
	return info, err
}

type waypoints struct{}

func (waypoints) Waypoints(context.Context, string) ([]markers.Waypoint, int, error) {
	return []markers.Waypoint{{Name: "A player's own", X: 12, Y: 64, Z: -30, Dimension: "overworld"}}, 0, nil
}

type surveyed struct{ last structures.Survey }

func (s surveyed) Last() (structures.Survey, bool) { return s.last, true }

// The made-up biomes, by block: 0 where nothing is generated.
func biomeAt(dimension string, x, z int) uint32 {
	if dimension == "nether" {
		if x < -32 || x >= 32 || z < -32 || z >= 32 {
			return 0
		}
		if x+z > 3 {
			return 178 // soulsand_valley
		}
		return 8 // hell
	}
	if x < -384 || x >= 384 || z < -384 || z >= 384 {
		return 0
	}
	switch {
	case x >= 320 && x < 336 && z >= 320 && z < 336:
		return 14 // mushroom island
	case x < -100 && z < -57:
		return 4 // forest
	case x+z > 37 && x >= 8:
		return 2 // desert
	}
	return 1 // plains
}

var grey = map[uint32]uint8{1: 60, 2: 200, 4: 120, 14: 250, 8: 90, 178: 160}

func biomeWorld(at time.Time) *biomes.World {
	columns := map[chunks.Pos][biomes.Columns]uint32{}
	surfaces := heights.NewBuilder(at, -1)
	add := func(d chunks.Dimension, name string, lo, hi int) {
		for cz := lo; cz < hi; cz++ {
			for cx := lo; cx < hi; cx++ {
				var c [biomes.Columns]uint32
				var h [heights.Columns]int16
				generated := false
				for i := range c {
					c[i] = biomeAt(name, cx*16+i&15, cz*16+i>>4)
					// Stored as the game stores it, counted from the floor: a
					// surface at y 64 and the odd hill of up to three more,
					// so that the markers at y 64 are on the ground.
					h[i] = int16(129 + (cx+cz)&3)
					generated = generated || c[i] != 0
				}
				if generated {
					columns[chunks.Pos{Dim: d, X: int32(cx), Z: int32(cz)}] = c
					_ = surfaces.AddColumns(d, int32(cx), int32(cz), &h)
				}
			}
		}
	}
	add(chunks.Overworld, "overworld", -24, 24)
	add(chunks.Nether, "nether", -2, 2)
	return biomes.Build(at, columns).WithHeights(surfaces.Map())
}

// terrain writes tiles shaded by biome, so that a tile is not one flat
// colour and a check can tell terrain from an empty square.
// harnessLevel is the nether's one extra cut. Its tiles are the default
// cut's in the opposite shades, so that they can be told from it.
var harnessLevel = render.NetherLevel{TopY: 66, Label: "Mid"}

func terrain(maps string, r *render.Unmined) error {
	const properties = "var UnminedMapProperties = {\n    minZoom: -4,\n    maxZoom: 0,\n    imageFormat: \"png\",\n    minRegionX: -1,\n    minRegionZ: -1,\n    maxRegionX: 0,\n    maxRegionZ: 0,\n}\n"
	for _, name := range []string{"overworld", "nether", harnessLevel.Dir()} {
		dimension, inverted := name, name == harnessLevel.Dir()
		if inverted {
			dimension = "nether"
		}
		dir := filepath.Join(maps, name)
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return err
		}
		if err := os.WriteFile(filepath.Join(dir, "unmined.map.properties.js"), []byte(properties), 0o644); err != nil {
			return err
		}
		for zoom := -4; zoom <= 0; zoom++ {
			blocks := float64(int(1) << uint(-zoom))
			n := max(2>>uint(-zoom), 1)
			for ty := -n; ty < n; ty++ {
				for tx := -n; tx < n; tx++ {
					img := image.NewNRGBA(image.Rect(0, 0, 256, 256))
					for py := range 256 {
						for px := range 256 {
							bx := int(math.Floor((float64(tx*256+px) + 0.5) * blocks))
							bz := int(math.Floor((float64(ty*256+py) + 0.5) * blocks))
							g := grey[biomeAt(dimension, bx, bz)]
							if inverted {
								g = 255 - g
							}
							img.SetNRGBA(px, py, color.NRGBA{g, g, g, 255})
						}
					}
					path := r.TilePath(dir, "png", zoom, tx, ty)
					if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
						return err
					}
					var b bytes.Buffer
					if err := png.Encode(&b, img); err != nil {
						return err
					}
					if err := os.WriteFile(path, b.Bytes(), 0o644); err != nil {
						return err
					}
				}
			}
		}
	}
	return nil
}

func square(side int, c color.NRGBA) ([]byte, error) {
	img := image.NewNRGBA(image.Rect(0, 0, side, side))
	for y := range side {
		for x := range side {
			img.SetNRGBA(x, y, c)
		}
	}
	var b bytes.Buffer
	err := png.Encode(&b, img)
	return b.Bytes(), err
}

var colours = []string{"white", "orange", "magenta", "light_blue", "yellow", "lime", "pink", "gray", "light_gray", "cyan", "purple", "blue", "brown", "green", "red", "black"}

var mobTypes = []string{"cow", "villager_v2", "zombie_villager_v2", "wolf", "cat", "zombie", "fox", "sheep", "strider", "iron_golem"}

// A face is made square with nothing where its width or height left room,
// and the head is only part of what is drawn: a nose hangs below it, ears
// stand above. These are the shapes that have been drawn off the middle of
// a plate before. side is the picture's, ink what is drawn of it and head
// the head within that, each x, y, width, height.
var faces = map[string]struct {
	side      int
	ink, head [4]int
}{
	// The odd column of room is one to the left and two to the right,
	// and the nose is the bottom row.
	"face/villager_v2":        {11, [4]int{1, 0, 8, 11}, [4]int{1, 0, 8, 10}},
	"face/zombie_villager_v2": {11, [4]int{1, 0, 8, 11}, [4]int{1, 0, 8, 10}},
	// Ears above the head, all of which fits its box.
	"face/fox":    {10, [4]int{0, 0, 10, 8}, [4]int{1, 2, 8, 6}},
	"face/cow":    {8, [4]int{0, 0, 8, 8}, [4]int{0, 0, 8, 8}},
	"face/zombie": {8, [4]int{0, 0, 8, 8}, [4]int{0, 0, 8, 8}},
}

// faced is the fetched art with where each face's head is, which the
// service works out from the models it fetches and the harness has none of.
type faced struct{ *icons.Mobs }

func (faced) Heads() map[string][4]int {
	heads := map[string][4]int{}
	for key, f := range faces {
		heads[key] = f.head
	}
	return heads
}

func inked(side int, ink [4]int, c color.NRGBA) ([]byte, error) {
	img := image.NewNRGBA(image.Rect(0, 0, side, side))
	for y := ink[1]; y < ink[1]+ink[3]; y++ {
		for x := ink[0]; x < ink[0]+ink[2]; x++ {
			img.SetNRGBA(x, y, c)
		}
	}
	var b bytes.Buffer
	err := png.Encode(&b, img)
	return b.Bytes(), err
}

// art is pictures and names made here, in the shape the service fetches
// them in: a flat square per mob type and per marker picture, and a name
// for each that is plainly not the game's.
func art() (icons.Set, error) {
	set := icons.Set{Mobs: map[string][]byte{}, Pictures: map[string][]byte{}, Lang: map[string]string{}, Entities: mobTypes}
	shade := func(n int) color.NRGBA {
		return color.NRGBA{uint8(40 + n*37%200), uint8(60 + n*59%180), uint8(80 + n*83%160), 255}
	}
	for i, kind := range mobTypes {
		pic, err := square(16, shade(i))
		if err != nil {
			return set, err
		}
		set.Mobs[kind] = pic
		set.Lang["entity."+kind+".name"] = "Harness " + kind
	}
	keys := []string{"container/chest", "container/trapped_chest", "container/barrel", "shulker/undyed", "marker/waypoint",
		"structure/village", "structure/monument", "structure/outpost", "structure/fortress"}
	for _, c := range colours {
		keys = append(keys, "bed/"+c, "shulker/"+c)
	}
	for i, key := range keys {
		pic, err := square(16, shade(i+20))
		if err != nil {
			return set, err
		}
		set.Pictures[key] = pic
	}
	n := 0
	for key, f := range faces {
		pic, err := inked(f.side, f.ink, shade(60+n))
		if err != nil {
			return set, err
		}
		set.Pictures[key] = pic
		n++
	}
	return set, nil
}

func markedWorld() markers.World {
	var overworld markers.Layer
	for i, c := range colours {
		overworld.Beds = append(overworld.Beds, markers.Marker{X: int32(-120 + i*16), Y: 64, Z: -100, Colour: c})
		overworld.Containers = append(overworld.Containers, markers.Marker{X: int32(-120 + i*16), Y: 64, Z: -70, Kind: "shulker", Colour: c})
	}
	overworld.Containers = append(overworld.Containers,
		markers.Marker{X: -120, Y: 64, Z: -40, Kind: "chest"},
		markers.Marker{X: -104, Y: 64, Z: -40, Kind: "chest", Trapped: true},
		markers.Marker{X: -88, Y: 30, Z: -40, Kind: "barrel", Name: "Harness barrel"},
		markers.Marker{X: -40, Y: 64, Z: -40, Kind: "chest", Name: hostile},
	)
	overworld.Mobs = []markers.Marker{
		{X: 31, Y: 65, Z: -17, Kind: "cat", Name: "Harness Cat One", ID: "101"},
		{X: 47, Y: 65, Z: -23, Kind: "sheep", Name: "Harness Sheep Two", Baby: true, ID: "102"},
		{X: 80, Y: 66, Z: -10, Kind: "villager_v2", Name: hostile, ID: "103"},
	}
	return markers.World{
		chunks.Overworld: &overworld,
		chunks.Nether: {
			Beds:       []markers.Marker{{X: -1, Y: 2, Z: -3, Colour: "blue"}},
			Containers: []markers.Marker{{X: 10, Y: 40, Z: -12, Kind: "chest", Name: "Harness nether chest"}},
			Mobs:       []markers.Marker{{X: 5, Y: 40, Z: 5, Kind: "strider", Name: "Harness Strider Four", Baby: true, ID: "201"}},
		},
	}
}

func survey(at time.Time) structures.Survey {
	box := func(x, z int32) structures.Box {
		return structures.Box{MinX: x, MinY: 60, MinZ: z, MaxX: x + 20, MaxY: 84, MaxZ: z + 20}
	}
	empty := func() *structures.Detail {
		return &structures.Detail{Mobs: []structures.MobCount{}, Named: []structures.NamedMob{}, SpawnerCounts: []structures.SpawnerCount{}, Spawners: []structures.Spawner{}, Containers: []structures.ContainerCount{}}
	}
	village := empty()
	village.MobsTotal = 9
	village.Mobs = []structures.MobCount{{Kind: "villager_v2", Count: 7, Babies: 1}, {Kind: "cat", Count: 2}}
	village.Named = []structures.NamedMob{{Kind: "villager_v2", Name: "Harness Librarian Five", Profession: "librarian", Level: 5}, {Kind: "villager_v2", Name: hostile, Profession: "farmer", Level: 1}}
	village.Containers = []structures.ContainerCount{{Kind: "chest", Unopened: 3, Holding: 2, Empty: 1}}
	village.Village = &structures.VillageDetail{
		Professions: []structures.Profession{{Profession: "farmer", Count: 2, Levels: [5]int{1, 0, 0, 0, 1}}, {Profession: "librarian", Count: 1, Levels: [5]int{0, 0, 0, 0, 1}}},
		Babies:      1, Golems: 1, Cats: 2,
		JobSites: []structures.JobSites{{Profession: "farmer", Count: 2}, {Profession: "librarian", Count: 1}},
		Met:      3,
	}
	monument := empty()
	monument.MobsTotal = 14
	monument.Mobs = []structures.MobCount{{Kind: "guardian", Count: 12}, {Kind: "elder_guardian", Count: 2}}
	outpost := empty()
	outpost.MobsTotal = 3
	outpost.Mobs = []structures.MobCount{{Kind: "pillager", Count: 3, Captains: 1}}
	return structures.Survey{
		At: at,
		Layers: map[chunks.Dimension]structures.Layer{
			chunks.Overworld: {
				Recorded: []structures.Structure{
					{Kind: structures.Village, Box: box(-120, 20), Village: &structures.VillageFacts{Counted: true, Villagers: 7, Golems: 1, Cats: 2, Beds: 9, Bells: 1, JobSites: 3}},
					{Kind: structures.Monument, Box: box(-70, 20), Areas: 22},
					{Kind: structures.Outpost, Box: box(-20, 20), Areas: 4},
				},
				Details: []*structures.Detail{village, monument, outpost},
				Predicted: []structures.Prediction{
					{Kind: structures.Village, X: -110, Z: 70}, {Kind: structures.Monument, X: -60, Z: 70},
					{Kind: structures.Village, X: -200, Z: -200, Candidate: true},
				},
			},
			chunks.Nether: {Recorded: []structures.Structure{{Kind: structures.Fortress, Box: structures.Box{MinX: -20, MinY: 48, MinZ: -25, MaxX: 10, MaxY: 72, MaxZ: 5}}}},
		},
		Check: structures.Check{State: structures.SeedVerified, Kinds: map[structures.Rule]structures.KindCheck{
			{Kind: structures.Monument, Dimension: chunks.Overworld}: {State: structures.SeedVerified, Agree: 4},
			{Kind: structures.Village, Dimension: chunks.Overworld}:  {State: structures.SeedVerified, Agree: 9, Disagree: 1},
		}},
		Level:    leveldat.Level{SpawnX: 40, SpawnZ: -72, SpawnY: 70, SpawnYKnown: true},
		HasLevel: true,
	}
}

// walker goes back and forth between two bounds along a line.
type walker struct{ x, z, dx, dz float64 }

func (w *walker) step(blocks float64) {
	w.x += w.dx * blocks
	w.z += w.dz * blocks
	if w.x > 150 || w.x < -150 {
		w.dx = -w.dx
		w.x += 2 * w.dx * blocks
	}
	if w.z > 150 || w.z < -150 {
		w.dz = -w.dz
		w.z += 2 * w.dz * blocks
	}
}

// What the pipeline between the game and the page does to a sample in
// production, which a tidy stream of whole lists once a second hides. It
// is the model web/testdata/stage.cjs runs the page's scripts against, fed
// here through the service's own assembly of records.
const (
	// The pack prints a list as lines of a few dozen items, and the list
	// is put together only when every line of it has come.
	partSize = 40
	// The pack keeps the mobs nearest the first player up to its cap, so
	// which mobs are listed changes as that player walks.
	mobCap = 150
	// A mob that unloads as it is read is left out of that one sample.
	skipOneIn = 23
	// A line now and then never arrives, and the list before it stands.
	loseOneIn = 41
	// The pack samples every second or every two, by how busy it is.
	paceEvery = 20
)

// herdHeights are where the herd stands, over a surface of about 64: most
// on the ground, and some in the air, in a cellar, in a cave and in the
// deepslate, so that every height band has something in it.
var herdHeights = []float64{64, 64, 64, 120, 45, 10, -20}

// herd is more mobs than the cap lets through, spread over the ground the
// first player walks.
func herd() []map[string]any {
	var mobs []map[string]any
	for i := range 220 {
		mobs = append(mobs, map[string]any{"i": strconv.Itoa(1000 + i), "t": mobTypes[i%len(mobTypes)], "x": float64(-190+(i%20)*20) + 0.5, "y": herdHeights[i%len(herdHeights)], "z": float64(-110+(i/20)*20) + 0.5})
	}
	return mobs
}

// play is the game: it says where everyone is as the pack does, through
// everything that happens to a sample on the way. A quiet world says only
// that it is still there, which is what a server with nobody on it sends.
func play(ctx context.Context, layer *live.Layer, recorder *trails.Recorder, quiet bool) {
	steve := &walker{x: -50, z: -20.5, dx: 0.94, dz: 0.34}
	fox := &walker{x: -100.5, z: 0.5, dx: -0.5, dz: 0.866}
	// A sprint: fast enough that a map which did not follow would be
	// left behind within the time a check watches for.
	const pace = 5.6
	crowd := herd()
	// Fixed, so that two runs are the same run.
	seed := uint32(20261011)
	chance := func(oneIn uint32) bool {
		seed = seed*1664525 + 1013904223
		return (seed>>8)%oneIn == 0
	}
	tick := time.NewTicker(time.Second)
	defer tick.Stop()
	for second, gen := 0, int64(0); ; second++ {
		var t time.Time
		select {
		case <-ctx.Done():
			return
		case t = <-tick.C:
		}
		slow := (second/paceEvery)%2 == 1
		if slow && second%2 == 1 {
			continue
		}
		gen++
		interval, seconds := 1000, 1.0
		if slow {
			interval, seconds = 2000, 2.0
		}
		records := []map[string]any{{"gen": gen, "kind": "tick", "scan": 1, "interval": interval}}
		if !quiet {
			steve.step(pace * seconds)
			fox.step(pace * seconds)
			players := []map[string]any{
				{"i": "1", "n": "HarnessPlayerOne", "x": steve.x, "y": 64, "z": steve.z, "r": 270},
				{"i": "7", "n": hostile, "x": 100.5, "y": 64, "z": -40.5, "r": 0},
			}
			mobs := []map[string]any{
				{"i": "52", "t": "wolf", "n": hostile, "x": 100.0, "y": 64, "z": 60.0},
				{"i": "101", "t": "cat", "n": "Harness Cat One", "x": 33.5, "y": 65, "z": -15.5},
				{"i": "61", "t": "fox", "n": "Harness Fox Three", "x": fox.x, "y": 64, "z": fox.z},
				{"i": "62", "t": "villager_v2", "x": steve.x + 6, "y": 64, "z": steve.z + 4},
				{"i": "63", "t": "zombie_villager_v2", "x": steve.x - 6, "y": 64, "z": steve.z + 4},
			}
			mobs = append(mobs, crowd...)
			far := func(m map[string]any) float64 {
				return math.Hypot(m["x"].(float64)-steve.x, m["z"].(float64)-steve.z)
			}
			sort.SliceStable(mobs, func(a, b int) bool { return far(mobs[a]) < far(mobs[b]) })
			var listed []map[string]any
			for _, m := range mobs {
				if len(listed) == mobCap {
					break
				}
				if !chance(skipOneIn) {
					listed = append(listed, m)
				}
			}
			for kind, items := range map[string][]map[string]any{"players": players, "mobs": listed} {
				parts := max(1, (len(items)+partSize-1)/partSize)
				for part := range parts {
					if kind == "mobs" && parts > 1 && chance(loseOneIn) {
						continue
					}
					records = append(records, map[string]any{"gen": gen, "kind": kind, "dim": "overworld", "part": part, "parts": parts,
						"items": items[part*partSize : min(len(items), (part+1)*partSize)]})
				}
			}
			records = append(records,
				map[string]any{"gen": gen, "kind": "players", "dim": "nether", "part": 0, "parts": 1, "items": []map[string]any{
					{"i": "9", "n": "HarnessPlayerTwo", "x": 12.5, "y": 40, "z": -7.5, "r": 90},
				}},
				map[string]any{"gen": gen, "kind": "mobs", "dim": "nether", "part": 0, "parts": 1, "items": []map[string]any{
					{"i": "201", "t": "strider", "n": "Harness Strider Four", "x": 6.5, "y": 40, "z": 6.5},
				}},
			)
			recorder.Record("overworld", t, []live.Entity{{ID: "1", Name: "HarnessPlayerOne", X: steve.x, Y: 64, Z: steve.z}})
		}
		for _, record := range records {
			raw, _ := json.Marshal(record)
			layer.Ingest(live.RawRecord{ID: gen, At: t, Data: string(raw)}, t)
		}
		layer.Flush(t)
	}
}

// harnessPlayer is the save's record of HarnessPlayerOne: made up, with a
// little of everything the details panel shows.
func harnessPlayer() players.Record {
	level, progress, air, warden := 12, 0.4, 280, 1
	item := func(slot int, name string, count int, tweak func(*players.Item)) players.Item {
		it := players.Item{Slot: slot, Name: name, Count: count}
		if tweak != nil {
			tweak(&it)
		}
		return it
	}
	return players.Record{
		Health:     &players.Gauge{Current: 17, Max: 20},
		Hunger:     &players.Gauge{Current: 14, Max: 20},
		Saturation: &players.Gauge{Current: 3.5, Max: 20},
		Absorption: &players.Gauge{Current: 4, Max: 16},
		Air:        &air,
		Level:      &level,
		Progress:   &progress,
		GameMode:   "survival",
		Dimension:  "overworld",
		Armor: []players.Item{
			item(0, "iron_helmet", 1, func(i *players.Item) {
				i.Damage, i.MaxDamage = 33, 165
				i.Enchants = []players.Enchantment{{ID: 0, Name: "Protection", Level: 4}, {ID: 17, Name: "Unbreaking", Level: 3}}
			}),
			item(2, "diamond_leggings", 1, nil),
		},
		Mainhand: &players.Item{Slot: 0, Name: "diamond_sword", Count: 1, Damage: 100, MaxDamage: 1561, Custom: "Harness Blade",
			Enchants: []players.Enchantment{{ID: 9, Name: "Sharpness", Level: 5}, {ID: 99, Level: 1}}},
		Inventory: []players.Item{
			item(0, "diamond_pickaxe", 1, func(i *players.Item) { i.Damage, i.MaxDamage = 500, 1561 }),
			item(1, "cobblestone", 64, nil),
			item(2, "bread", 12, nil),
			item(9, "chest", 3, nil),
			item(10, "pig_spawn_egg", 1, nil),
			item(30, "oak_planks", 7, nil),
		},
		EnderChest: []players.Item{item(0, "ender_pearl", 16, nil), item(26, "shulker_box", 1, nil)},
		Death:      &players.Place{X: 12, Y: 64, Z: -34, Dimension: "overworld"},
		Spawn:      &players.Place{X: -5, Y: 65, Z: 8, Dimension: "overworld"},

		RecipesUnlocked: 120,
		SeenCredits:     true,
		WardenThreat:    &warden,
	}
}
