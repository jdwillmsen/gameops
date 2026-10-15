// Command layerbench measures the cave and level views against a copy of a
// real world, and draws sample tiles to look at. It reads the world and
// writes only to -out; nothing it prints or writes belongs in the repository.
//
//	layerbench -world /path/to/FWB -mode names
//	layerbench -world /path/to/FWB -mode sample -dim nether -cut 100 -style floor
//	layerbench -world /path/to/FWB -mode pass   -dim overworld -cut 0
//	layerbench -world /path/to/FWB -mode floors -out /tmp/mcmap-underground
//	layerbench -world /path/to/FWB -mode png    -dim overworld -cut 0 -out /tmp/mcmap-underground
//
// Run it with one core, as the map's pod has, which is what -procs 1 does.
package main

import (
	"bufio"
	"context"
	"flag"
	"fmt"
	"math/rand/v2"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"time"

	"github.com/df-mc/goleveldb/leveldb/util"

	"github.com/jdwillmsen/gameops/minecraft/mcmap/internal/chunks"
	"github.com/jdwillmsen/gameops/minecraft/mcmap/internal/layers"
	"github.com/jdwillmsen/gameops/minecraft/mcmap/internal/topo"
)

func main() {
	world := flag.String("world", "", "a copy of the world: the directory holding db")
	mode := flag.String("mode", "sample", "names, sample, pass, floors or png")
	dimName := flag.String("dim", "overworld", "overworld, nether or end")
	cut := flag.Int("cut", 0, "the highest y drawn")
	thickness := flag.Int("thickness", layers.DefaultThickness, "blocks in the band")
	styleName := flag.String("style", "cut", "cut or floor")
	count := flag.Int("n", 24, "tiles to time per zoom in sample mode")
	out := flag.String("out", "", "directory for pictures")
	tileX := flag.Int("tx", 0, "probe mode: tile x at zoom 0")
	tileY := flag.Int("ty", 0, "probe mode: tile y at zoom 0")
	pxX := flag.Int("px", 0, "probe mode: pixel x in the tile")
	pxZ := flag.Int("pz", 0, "probe mode: pixel y in the tile")
	procs := flag.Int("procs", 1, "cores to use")
	flag.Parse()
	runtime.GOMAXPROCS(*procs)
	if *world == "" {
		fatal("-world is required")
	}
	dim := map[string]chunks.Dimension{"overworld": chunks.Overworld, "nether": chunks.Nether, "end": chunks.End}[*dimName]
	if _, ok := map[string]bool{"overworld": true, "nether": true, "end": true}[*dimName]; !ok {
		fatal("unknown dimension")
	}
	style := layers.Cut
	if *styleName == "floor" {
		style = layers.Floor
	}
	spec := layers.Spec{Dim: dim, Cut: *cut, Thickness: *thickness, Style: style}

	work, err := os.MkdirTemp("", "layerbench-")
	if err != nil {
		fatal(err.Error())
	}
	defer os.RemoveAll(work)
	dbDir := filepath.Join(*world, "db")

	began := time.Now()
	set, err := chunks.Scan(context.Background(), dbDir, filepath.Join(work, "scan"))
	if err != nil {
		fatal(err.Error())
	}
	scanned := time.Since(began)
	db, closeDB, err := chunks.OpenView(dbDir, filepath.Join(work, "view"))
	if err != nil {
		fatal(err.Error())
	}
	defer closeDB()
	reg := layers.NewRegistry()
	r := &layers.Renderer{Reader: layers.Reader{DB: db, Registry: reg}}
	fmt.Printf("chunks: %d overworld, %d nether, %d end (key scan %s)\n",
		count1(set, chunks.Overworld), count1(set, chunks.Nether), count1(set, chunks.End), scanned.Round(time.Millisecond))

	switch *mode {
	case "names":
		names(&r.Reader)
	case "sample":
		sample(r, set, spec, *count)
	case "pass":
		pass(r, set, spec)
	case "floors":
		floors(r, set, *out)
	case "probe":
		probe(r, spec, *tileX, *tileY, *pxX, *pxZ)
	case "png":
		pngs(r, set, spec, *out)
	default:
		fatal("unknown mode")
	}
	fmt.Printf("peak resident memory: %.0f MB; heap in use at end: %.0f MB\n", peakRSS(), heapMB())
}

func fatal(msg string) {
	fmt.Fprintln(os.Stderr, msg)
	os.Exit(1)
}

func count1(set chunks.Set, d chunks.Dimension) int {
	n := 0
	for p := range set {
		if p.Dim == d {
			n++
		}
	}
	return n
}

// peakRSS is the process's high-water resident memory in MB.
func peakRSS() float64 {
	f, err := os.Open("/proc/self/status")
	if err != nil {
		return 0
	}
	defer f.Close()
	s := bufio.NewScanner(f)
	for s.Scan() {
		if rest, ok := strings.CutPrefix(s.Text(), "VmHWM:"); ok {
			var kb float64
			fmt.Sscanf(strings.TrimSpace(rest), "%f", &kb)
			return kb / 1024
		}
	}
	return 0
}

func heapMB() float64 {
	var m runtime.MemStats
	runtime.ReadMemStats(&m)
	return float64(m.HeapInuse) / (1 << 20)
}

// names walks the world once, the way a full pass would, and lists the
// blocks that have no colour of their own.
func names(r *layers.Reader) {
	began := time.Now()
	n := 0
	for _, d := range chunks.Dimensions {
		must(r.Walk(context.Background(), d, func(*layers.Column) error { n++; return nil }))
	}
	fmt.Printf("walked %d chunk columns of all three dimensions in %s\n", n, time.Since(began).Round(time.Millisecond))
	blocks := r.Registry.Blocks()
	var by [3][]string
	for _, b := range blocks {
		by[b.Source] = append(by[b.Source], b.Name)
	}
	fmt.Printf("%d kinds of block: %d listed, %d derived, %d guessed\n", len(blocks), len(by[layers.Listed]), len(by[layers.Derived]), len(by[layers.Guessed]))
	slices.Sort(by[layers.Guessed])
	fmt.Println("guessed:", strings.Join(by[layers.Guessed], " "))
}

type tileAt struct{ x, y int }

// occupied counts chunks in each tile of the zoom, and returns the tiles
// that have at least min of them, densest first.
func occupied(set chunks.Set, d chunks.Dimension, zoom, min int) []tileAt {
	chunksAcross := 16 << -zoom
	counts := map[tileAt]int{}
	for p := range set {
		if p.Dim != d {
			continue
		}
		counts[tileAt{floorDiv(int(p.X), chunksAcross), floorDiv(int(p.Z), chunksAcross)}]++
	}
	var out []tileAt
	for t, c := range counts {
		if c >= min {
			out = append(out, t)
		}
	}
	slices.SortFunc(out, func(a, b tileAt) int {
		if c := counts[b] - counts[a]; c != 0 {
			return c
		}
		if a.x != b.x {
			return a.x - b.x
		}
		return a.y - b.y
	})
	return out
}

func floorDiv(a, b int) int {
	q := a / b
	if a%b != 0 && (a < 0) != (b < 0) {
		q--
	}
	return q
}

type stats []float64

func (s stats) String() string {
	slices.Sort(s)
	var sum float64
	for _, v := range s {
		sum += v
	}
	n := len(s)
	return fmt.Sprintf("mean %7.2f  median %7.2f  p95 %7.2f", sum/float64(n), s[n/2], s[min(n-1, n*95/100)])
}

func ms(d time.Duration) float64 { return float64(d.Microseconds()) / 1000 }

// sample times tiles drawn on request at zoom 0 and -1, from among the
// tiles with most of their chunks generated and a spread of the rest.
func sample(r *layers.Renderer, set chunks.Set, spec layers.Spec, n int) {
	rng := rand.New(rand.NewPCG(7, 7))
	for _, zoom := range []int{0, -1} {
		full := (16 << -zoom) * (16 << -zoom)
		tiles := occupied(set, spec.Dim, zoom, full/2)
		if len(tiles) == 0 {
			tiles = occupied(set, spec.Dim, zoom, 1)
		}
		rng.Shuffle(len(tiles), func(i, j int) { tiles[i], tiles[j] = tiles[j], tiles[i] })
		tiles = tiles[:min(n, len(tiles))]
		var read, paint, enc, total, bytes, nchunks stats
		for _, t := range tiles {
			began := time.Now()
			_, tm, ok, err := r.Tile(spec, zoom, t.x, t.y)
			if err != nil {
				fatal(err.Error())
			}
			if !ok {
				continue
			}
			total = append(total, ms(time.Since(began)))
			read, paint, enc = append(read, ms(tm.Read)), append(paint, ms(tm.Paint)), append(enc, ms(tm.Encode))
			bytes = append(bytes, float64(tm.Bytes))
			nchunks = append(nchunks, float64(tm.Chunks))
		}
		if len(total) == 0 {
			fmt.Printf("zoom %2d: no tiles\n", zoom)
			continue
		}
		fmt.Printf("%s cut %d %s zoom %2d, %d tiles (ms; chunks and bytes per tile)\n", spec.Dim.Name(), spec.Cut, styleName(spec.Style), zoom, len(total))
		fmt.Printf("  read    %s\n  paint   %s\n  encode  %s\n  total   %s\n  bytes   %s\n  chunks  %s\n", read, paint, enc, total, bytes, nchunks)
	}
}

func styleName(s layers.Style) string {
	if s == layers.Floor {
		return "floor"
	}
	return "cut"
}

// pass draws every tile of a dimension that holds a chunk at a block to a
// pixel, as pre-rendering the dimension would, and reports the cost; then
// reads every chunk and does nothing with it, which is the least a pass
// over the dimension can cost.
func pass(r *layers.Renderer, set chunks.Set, spec layers.Spec) {
	tiles := occupied(set, spec.Dim, 0, 1)
	var tm layers.Timings
	var bytes, drawn int
	began := time.Now()
	for _, t := range tiles {
		_, one, ok, err := r.Tile(spec, 0, t.x, t.y)
		if err != nil {
			fatal(err.Error())
		}
		tm.Read += one.Read
		tm.Paint += one.Paint
		tm.Encode += one.Encode
		tm.Chunks += one.Chunks
		if ok {
			drawn++
			bytes += one.Bytes
		}
	}
	fmt.Printf("%s cut %d %s: %d tiles of %d occupied drawn at zoom 0 in %s\n", spec.Dim.Name(), spec.Cut, styleName(spec.Style), drawn, len(tiles), time.Since(began).Round(100*time.Millisecond))
	fmt.Printf("  read %s (%d chunk reads), paint %s, encode %s; %.1f MB of PNG, %.1f KB per tile\n",
		tm.Read.Round(100*time.Millisecond), tm.Chunks, tm.Paint.Round(100*time.Millisecond), tm.Encode.Round(100*time.Millisecond),
		float64(bytes)/(1<<20), float64(bytes)/1024/float64(max(drawn, 1)))

	began = time.Now()
	n := 0
	must(r.Walk(context.Background(), spec.Dim, func(*layers.Column) error { n++; return nil }))
	fmt.Printf("  walking and decoding every chunk once, drawing nothing: %d chunks in %s\n", n, time.Since(began).Round(100*time.Millisecond))
}

// floors builds the Nether's floor maps for its three levels in one pass
// and draws a topography tile of each.
func floors(r *layers.Renderer, set chunks.Set, out string) {
	cuts := []int{100, 66, 34}
	began := time.Now()
	maps, err := layers.FloorMaps(context.Background(), &r.Reader, chunks.Nether, cuts, time.Now())
	if err != nil {
		fatal(err.Error())
	}
	fmt.Printf("floor maps of the nether at %v: %s; ", cuts, time.Since(began).Round(100*time.Millisecond))
	for i, m := range maps {
		fmt.Printf("level %d holds %d chunks in %.1f MB; ", cuts[i], m.Chunks(chunks.Nether), float64(m.Bytes())/(1<<20))
	}
	fmt.Println()
	if out == "" {
		return
	}
	if err := os.MkdirAll(out, 0o755); err != nil {
		fatal(err.Error())
	}
	tiles := occupied(set, chunks.Nether, 0, 100)
	for i, m := range maps {
		for _, zoom := range []int{0, -1} {
			ts := occupied(set, chunks.Nether, zoom, (16<<-zoom)*(16<<-zoom)/3)
			if len(ts) == 0 {
				ts = tiles
			}
			t := ts[0]
			began := time.Now()
			data, ok := topo.FloorTile(m, chunks.Nether, zoom, t.x, t.y, topo.Pick{Styles: topo.Shade | topo.Contours | topo.Tint})
			if !ok {
				continue
			}
			fmt.Printf("  topography of level %d zoom %d: %.2f ms, %d bytes\n", cuts[i], zoom, ms(time.Since(began)), len(data))
			must(os.WriteFile(filepath.Join(out, fmt.Sprintf("nether-topo-level%d-zoom%d.png", cuts[i], zoom)), data, 0o644))
		}
	}
}

// pngs draws a few tiles of a cut, the densest and a couple more, at zoom 0
// and -1.
func pngs(r *layers.Renderer, set chunks.Set, spec layers.Spec, out string) {
	if out == "" {
		fatal("-out is required")
	}
	must(os.MkdirAll(out, 0o755))
	for _, zoom := range []int{0, -1} {
		full := (16 << -zoom) * (16 << -zoom)
		tiles := occupied(set, spec.Dim, zoom, full*3/4)
		if len(tiles) == 0 {
			tiles = occupied(set, spec.Dim, zoom, 1)
		}
		picks := []int{0, len(tiles) / 3, 2 * len(tiles) / 3}
		for n, at := range picks {
			if at >= len(tiles) {
				continue
			}
			t := tiles[at]
			data, _, ok, err := r.Tile(spec, zoom, t.x, t.y)
			if err != nil || !ok {
				continue
			}
			fmt.Printf("sample %d at zoom %d is tile %d, %d\n", n+1, zoom, t.x, t.y)
			name := fmt.Sprintf("%s-%s-cut%d-t%d-z%d-sample%d.png", spec.Dim.Name(), styleName(spec.Style), spec.Cut, spec.Thickness, zoom, n+1)
			must(os.WriteFile(filepath.Join(out, name), data, 0o644))
			fmt.Println("wrote", filepath.Join(out, name))
		}
	}
}

func must(err error) {
	if err != nil {
		fatal(err.Error())
	}
}

// probe names what a pixel of a zoom-0 tile shows, and the blocks in its
// column, to find out why a tile looks as it does.
func probe(r *layers.Renderer, spec layers.Spec, tx, ty, px, pz int) {
	x, z := tx*layers.TileSize+px, ty*layers.TileSize+pz
	col := layers.GetColumn()
	found, err := r.Read(chunks.Pos{Dim: spec.Dim, X: int32(x >> 4), Z: int32(z >> 4)}, col)
	fmt.Println("found", found, "err", err, "slices", col.Slices)
	prefix := chunks.RecordKey(chunks.Pos{Dim: spec.Dim, X: int32(x >> 4), Z: int32(z >> 4)}, 0)
	prefix = prefix[:len(prefix)-1]
	it := r.DB.NewIterator(util.BytesPrefix(prefix), nil)
	for it.Next() {
		if spec.Dim == chunks.Overworld && len(it.Key()) > 10 {
			continue
		}
		fmt.Printf("record tag 0x%02x, %d key bytes, %d value bytes\n", it.Key()[len(prefix)], len(it.Key()), len(it.Value()))
	}
	it.Release()
	blocks := r.Registry.Blocks()
	for y := spec.Cut + 16; y >= layers.MinY(spec.Dim) && y >= spec.Cut-40; y-- {
		id := col.ID(x&15, y, z&15)
		fmt.Printf("y %d: %s\n", y, blocks[id].Name)
	}
}
