package icons

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"image"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus/testutil"
)

// chained is a controller file whose arrays name one another: each of
// depth arrays holds entries, and each entry is the sum of width entries
// of the next. It is a few kilobytes, and read naively is width to the
// power of depth evaluations.
func chained(depth, entries, width int) []byte {
	arrays := map[string][]string{}
	for level := range depth {
		var list []string
		for range entries {
			var terms []string
			for n := range width {
				if level == depth-1 {
					terms = append(terms, "Texture.x")
				} else {
					terms = append(terms, fmt.Sprintf("Array.a%d[%d]", level+1, n%entries))
				}
			}
			list = append(list, strings.Join(terms, "+"))
		}
		arrays[fmt.Sprintf("Array.a%d", level)] = list
	}
	raw, _ := json.Marshal(map[string]any{"render_controllers": map[string]any{"controller.render.chained": map[string]any{
		"geometry": "Geometry.default", "textures": []string{"Array.a0[0]"}, "arrays": map[string]any{"textures": arrays},
	}}})
	return raw
}

// Measured before there was a count of the work: 2 ms at two levels, 191
// ms at three, 15.5 s at four, and six levels are within every other
// limit. Each must now be read, or refused, in milliseconds.
func TestAControllerWhoseArraysNameOneAnotherIsReadOnceNotExponentially(t *testing.T) {
	for _, depth := range []int{2, 3, 4, 6} {
		started := time.Now()
		controllers, err := parseControllers(t.Context(), chained(depth, 1, 80))
		if err != nil {
			t.Fatal(err)
		}
		c := controllers["controller.render.chained"]
		name, ok := c.pick(c.Textures[0])
		took := time.Since(started)
		t.Logf("%d levels of eighty: %s", depth, took.Round(10*time.Microsecond))
		if took > 250*time.Millisecond {
			t.Errorf("%d levels took %s", depth, took)
		}
		// The sum of eighty textures is no texture.
		if ok {
			t.Errorf("%d levels came to %q", depth, name)
		}
		// Each array's one entry was read once: its eighty operands, and
		// not eighty times those of the level below.
		if spent := maxFileSteps - c.work.file; spent > depth*200 {
			t.Errorf("%d levels took %d steps, want each entry read once", depth, spent)
		}
	}
}

func TestAControllerThatTakesTooMuchWorkIsGivenUpOnAndNotReadAgain(t *testing.T) {
	// Twenty entries a level, each the sum of eighty of the next: reading
	// each once is still over the allowance for one expression.
	controllers, err := parseControllers(t.Context(), chained(4, 20, 80))
	if err != nil {
		t.Fatal(err)
	}
	c := controllers["controller.render.chained"]
	started := time.Now()
	if _, err := c.evaluate(c.Textures[0], 0); !errors.Is(err, errSpent) {
		t.Fatalf("an expression past its allowance ended with %v", err)
	}
	if took := time.Since(started); took > 250*time.Millisecond {
		t.Errorf("giving up took %s", took)
	}
	if spent := maxFileSteps - c.work.file; spent > maxExpressionSteps+1 {
		t.Errorf("%d steps were spent on an expression allowed %d", spent, maxExpressionSteps)
	}
	// Nothing more is read from that file: its mobs fall back, and it is
	// not tried again at the next question.
	before := c.work.file
	if name, ok := c.pick("Texture.default"); ok {
		t.Errorf("a controller given up on still answered %q", name)
	}
	if c.hides("Bridle") || c.work.file != before {
		t.Error("a controller given up on was read again")
	}
	// A file may not spend more than its own allowance either, however
	// many expressions it is spread over.
	work := newEffort(t.Context())
	plain := controller{work: work}
	for range maxFileSteps {
		if _, err := plain.evaluate("1+1+1+1+1+1+1+1", 0); err != nil {
			break
		}
	}
	if !work.spent || work.file > 0 {
		t.Errorf("a file's allowance was never spent: %d left", work.file)
	}
}

func TestReadingAControllerStopsWhenItsContextDoes(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	controllers, err := parseControllers(ctx, []byte(syntheticControllers))
	if err != nil {
		t.Fatal(err)
	}
	c := controllers["controller.render.plain"]
	if _, ok := c.pick(c.Geometry); !ok {
		t.Fatal("the controller does not read while its context lives")
	}
	cancel()
	if name, ok := c.pick(c.Geometry); ok {
		t.Errorf("after its context ended the controller still answered %q", name)
	}
	// And one that ends part way through a long read stops it.
	ctx, cancel = context.WithCancel(t.Context())
	work := newEffort(ctx)
	long := controller{work: work}
	cancel()
	work.file = 512
	for range 4 {
		_, _ = long.evaluate(strings.Repeat("1+", 200)+"1", 0)
	}
	if !work.spent || work.file < 100 {
		t.Errorf("a read went on after its context ended: spent %v, %d left", work.spent, work.file)
	}
}

func TestExpressionsAndArraysAreHeldToTheirLimits(t *testing.T) {
	file := func(arrays, body string) []byte {
		return []byte(`{"render_controllers": {"controller.render.x": {"geometry": "Geometry.default", "textures": ["Texture.default"], "arrays": {"textures": {` + arrays + `}}` + body + `}}}`)
	}
	read := func(raw []byte) (controller, bool) {
		controllers, err := parseControllers(t.Context(), raw)
		if err != nil {
			return controller{}, false
		}
		c, ok := controllers["controller.render.x"]
		return c, ok
	}
	// An entry that names itself, directly and round a ring.
	for name, arrays := range map[string]string{
		"itself":      `"Array.a": ["Array.a[0]"]`,
		"a ring":      `"Array.a": ["Array.b[0]"], "Array.b": ["Array.a[0]"]`,
		"over depth":  chain(maxExpressionDepth + 2),
		"a long line": `"Array.a": ["` + strings.Repeat("1+", maxExpression) + `1"]`,
	} {
		c, ok := read(file(arrays, ""))
		if !ok {
			t.Fatalf("%s: the controller was not read at all", name)
		}
		started := time.Now()
		if got, ok := c.pick("Array.a[0]"); ok || time.Since(started) > 100*time.Millisecond {
			t.Errorf("%s came to %q in %s", name, got, time.Since(started))
		}
	}
	// A chain within the depth allowed is followed to its end.
	if c, _ := read(file(chain(6), "")); true {
		if got, ok := c.pick("Array.a0[0]"); !ok || got != "end" {
			t.Errorf("a chain within the limit came to %q %v", got, ok)
		}
	}
	// Arrays of the same name in two controllers of a file are not one.
	both, err := parseControllers(t.Context(), []byte(`{"render_controllers": {
		"controller.render.one": {"arrays": {"textures": {"Array.skins": ["Texture.first"]}}},
		"controller.render.two": {"arrays": {"textures": {"Array.skins": ["Texture.second"]}}}}}`))
	if err != nil {
		t.Fatal(err)
	}
	one, _ := both["controller.render.one"].pick("Array.skins[0]")
	two, _ := both["controller.render.two"].pick("Array.skins[0]")
	if one != "first" || two != "second" {
		t.Errorf("two controllers' arrays of one name came to %q and %q", one, two)
	}
	// Too many arrays, or too many controllers, and it is left out.
	var many []string
	for n := range maxArrays + 1 {
		many = append(many, fmt.Sprintf(`"Array.n%d": ["Texture.x"]`, n))
	}
	if _, ok := read(file(strings.Join(many, ","), "")); ok {
		t.Error("a controller with too many arrays was kept")
	}
	var crowd []string
	for n := range maxFileControllers + 1 {
		crowd = append(crowd, fmt.Sprintf(`"controller.render.c%d": {"geometry": "Geometry.default"}`, n))
	}
	if _, err := parseControllers(t.Context(), []byte(`{"render_controllers": {`+strings.Join(crowd, ",")+`}}`)); err == nil {
		t.Error("a file with too many controllers was read")
	}
}

// chain is arrays a0 to a(n-1), each naming the next, ending in a texture.
func chain(n int) string {
	var arrays []string
	for i := range n {
		entry := fmt.Sprintf("Array.a%d[0]", i+1)
		if i == n-1 {
			entry = "Texture.end"
		}
		name := fmt.Sprintf("Array.a%d", i)
		if i == 0 && n > maxExpressionDepth {
			arrays = append(arrays, fmt.Sprintf(`"Array.a": [%q]`, "Array.a0[0]"))
		}
		arrays = append(arrays, fmt.Sprintf(`%q: [%q]`, name, entry))
	}
	return strings.Join(arrays, ",")
}

func TestAModelInheritsThroughOnlySoManyOthers(t *testing.T) {
	models := map[string]model{}
	for i := range maxInheritance + 3 {
		m := model{id: fmt.Sprintf("geometry.m%d", i)}
		if i > 0 {
			m.parent = fmt.Sprintf("geometry.m%d", i-1)
		}
		models[m.id] = m
	}
	if _, err := resolved(models, fmt.Sprintf("geometry.m%d", maxInheritance)); err != nil {
		t.Errorf("a model inheriting through %d others was refused: %v", maxInheritance, err)
	}
	if _, err := resolved(models, fmt.Sprintf("geometry.m%d", maxInheritance+2)); err == nil {
		t.Errorf("a model inheriting through %d others was resolved", maxInheritance+2)
	}
}

// A model of a thousand boxes drawn by a definition that names one
// controller ten thousand times took 31 seconds and built forty million
// entries before it was counted as it went.
func TestADefinitionCannotMakeAFaceOfMoreLayersThanAreKept(t *testing.T) {
	var cubes []string
	for range maxModelCubes - 1 {
		cubes = append(cubes, `{"origin": [-3, 1, -5], "size": [4, 4, 1], "uv": [0, 0]}`)
	}
	file := `{"geometry.heavy": {"texturewidth": 64, "textureheight": 64, "bones": [{"name": "head", "cubes": [{"origin": [-4, 0, -4], "size": [8, 8, 8], "uv": [0, 0]}]}`
	for i := 0; i < len(cubes); i += maxBoneCubes {
		file += fmt.Sprintf(`, {"name": "b%d", "parent": "head", "cubes": [%s]}`, i, strings.Join(cubes[i:min(i+maxBoneCubes, len(cubes))], ","))
	}
	file += `]}}`
	models := modelsOf(t, file)
	if len(models) != 1 {
		t.Fatal("the heavy model was refused; the test would prove nothing")
	}
	controllers, _ := parseControllers(t.Context(), []byte(syntheticControllers))
	look := appearance{Textures: map[string]string{"default": "textures/entity/heavy"}, Geometry: map[string]string{"default": "geometry.heavy"}}
	for range 10_000 {
		look.Controllers = append(look.Controllers, json.RawMessage(`"controller.render.plain"`))
	}
	if n := len(look.controllers()); n != maxDefinitionControllers {
		t.Errorf("a definition naming ten thousand controllers is drawn by %d, want the first %d", n, maxDefinitionControllers)
	}
	gated := appearance{Controllers: []json.RawMessage{json.RawMessage(`{` + strings.TrimSuffix(strings.Repeat(`"controller.render.plain": "1",`, 1), ",") + `}`)}}
	if n := len(gated.controllers()); n != 1 {
		t.Errorf("a controller listed with a condition that holds is drawn %d times", n)
	}
	started := time.Now()
	_, err := library{models: models, controllers: controllers}.face(look, override{})
	if took := time.Since(started); took > 2*time.Second {
		t.Errorf("the face took %s to refuse", took)
	}
	// Refused as they are gathered, and not after all of them have been.
	if err == nil || !strings.Contains(err.Error(), fmt.Sprintf("over %d layers", maxLayers)) {
		t.Errorf("a face of thousands of layers: %v", err)
	}
	// One box over the model's limit and the model is not read at all.
	over := strings.Replace(file, `{"name": "head", "cubes": [`, `{"name": "head", "cubes": [{"origin": [0,0,0], "size": [1,1,1], "uv": [0,0]}, {"origin": [0,0,0], "size": [1,1,1], "uv": [0,0]},`, 1)
	if got := modelsOf(t, over); len(got) != 0 {
		t.Error("a model over the limit on boxes was read")
	}
}

func TestTexturesAreHeldToASideAScaleAndACountOfPixels(t *testing.T) {
	small := asPNG(t, painted(64, 64))
	if _, err := decodeTexture("textures/entity/x", declaring(small, maxModelTextureSide+1, 64)); err == nil {
		t.Error("a texture one pixel over the widest allowed was decoded")
	}
	if _, err := decodeTexture("textures/entity/x", declaring(small, 60000, 60000)); err == nil {
		t.Error("a texture declaring thousands of megapixels was decoded")
	}
	if _, err := decodeTGA(tga(maxModelTextureSide+1, 1, 2, 0, nil), maxModelTextureSide); err == nil {
		t.Error("a TGA over the widest allowed was decoded")
	}
	r := walker(t)
	if _, err := r.render(map[string]*image.NRGBA{"textures/entity/walker": painted(64*(maxTextureScale+1), 32*(maxTextureScale+1))}); err == nil {
		t.Error("a texture finer than any pack is drawn was drawn from")
	}
	if _, err := r.render(map[string]*image.NRGBA{"textures/entity/walker": painted(64*maxTextureScale, 32*maxTextureScale)}); err != nil {
		t.Errorf("a texture at the finest allowed was refused: %v", err)
	}

	// Decoded one picture at a time: what one picture needed is let go
	// once it comes to more than is kept between two.
	side := maxModelTextureSide
	body := asPNG(t, image.NewNRGBA(image.Rect(0, 0, side, side)))
	store := &textureStore{bodies: map[string][]byte{}, held: map[string]*image.NRGBA{}, refused: map[string]error{}}
	count := maxFetchPixels/(side*side) + 4
	for n := range count {
		store.bodies["t"+strconv.Itoa(n)] = body
	}
	most := 0
	for n := range count {
		held := store.of([]string{"t" + strconv.Itoa(n)})
		most = max(most, len(held))
	}
	if limit := maxHeldPixels/(side*side) + 2; most > limit {
		t.Errorf("%d textures were held at once, want no more than %d", most, limit)
	}
	// And no more than a fetch's allowance is decoded in all.
	if store.decoded > maxFetchPixels+side*side {
		t.Errorf("%d pixels were decoded, over the %d a fetch is allowed", store.decoded, maxFetchPixels)
	}
	if err := store.refused["t"+strconv.Itoa(count-1)]; !errors.Is(err, errTooManyPixels) {
		t.Errorf("the texture past the allowance: %v", err)
	}
}

func TestMorePicturesThanAFetchMayNameTexturesForAreNotMade(t *testing.T) {
	s := newSamples(t)
	var asked atomic.Int64
	inner := s.srv.Config.Handler
	s.srv.Config.Handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		asked.Add(1)
		inner.ServeHTTP(w, r)
	})
	recipes := map[string]Recipe{}
	var keys []string
	for n := range maxModelTextures + 1 {
		key := fmt.Sprintf("face/m%d", n)
		recipes[key], keys = Recipe{Flat: fmt.Sprintf("textures/items/t%d", n)}, append(keys, key)
	}
	set := s.source().make(t.Context(), recipes, keys, &budget{left: maxTotalBytes})
	if len(set.Pictures) != 0 || len(set.Rejected) != len(keys) || asked.Load() != 0 {
		t.Errorf("%d pictures, %d rejected, %d requests for more textures than a fetch may name", len(set.Pictures), len(set.Rejected), asked.Load())
	}
}

func TestAFaultInOneMobsFaceCostsOnlyThatMob(t *testing.T) {
	s := newSamples(t)
	withMobs(t, s)
	faultIn = func(step string) {
		if step == "face/grazer" || step == "picture/block/chest" {
			var none []int
			_ = none[3]
		}
	}
	t.Cleanup(func() { faultIn = nil })
	before := testutil.ToFloat64(metricFaults)
	said := &heard{}
	source := s.source()
	source.Logger = slogOf(said)
	set, err := source.Fetch(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := set.Pictures["face/blaze"]; !ok {
		t.Error("the other mob's face was lost")
	}
	if _, ok := set.Pictures["block/barrel"]; !ok {
		t.Error("the other blocks were lost")
	}
	for _, key := range []string{"face/grazer", "block/chest"} {
		if _, made := set.Pictures[key]; made || set.Rejected[key] == "" || slices.Contains(set.Missing, key) {
			t.Errorf("%s: made %v, rejected %q, missing %v", key, made, set.Rejected[key], slices.Contains(set.Missing, key))
		}
	}
	if got := testutil.ToFloat64(metricFaults) - before; got != 2 {
		t.Errorf("%v faults counted, want the two", got)
	}
	if n := said.count(slog.LevelError); n != 2 {
		t.Errorf("%d error lines for two faults", n)
	}
}

func TestAFaultInTheWholeOfTheMakingLeavesTheIconsAndIsNotRetried(t *testing.T) {
	s := newSamples(t)
	withMobs(t, s)
	faultIn = func(step string) {
		if step == artPlan {
			panic("the models are not what this reads")
		}
	}
	t.Cleanup(func() { faultIn = nil })
	source := s.source()
	source.Logger = quiet()
	set, err := source.Fetch(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if len(set.Mobs) != 5 || len(set.Pictures) != len(pictureKeys()) || len(set.Lang) == 0 {
		t.Errorf("%d mob icons, %d pictures: the fault cost what does not depend on it", len(set.Mobs), len(set.Pictures))
	}
	if slices.Contains(set.Missing, artPlan) || set.Rejected[artPlan] == "" {
		t.Errorf("missing %v, rejected %v: a fault that will happen again is asked for again", set.Missing, set.Rejected)
	}
}

func TestAFaultInAFetchOrAFillIsAFailureAndWhatIsHeldIsStillServed(t *testing.T) {
	dir := t.TempDir()
	first := &Mobs{Dir: dir, Ref: testRef, Logger: quiet(), Fetch: func(context.Context) (Set, error) {
		return Set{Mobs: map[string][]byte{"cow": picture(t, 16, 16, red)}, Pictures: map[string][]byte{"container/chest": picture(t, 16, 16, green)}, Missing: []string{"bed/red"}}, nil
	}}
	first.Run(t.Context())
	var fills atomic.Int64
	m := &Mobs{Dir: dir, Ref: testRef, Logger: quiet(), Fetch: neverFetch(t), RetryMin: time.Millisecond, RetryMax: 2 * time.Millisecond, RefillEvery: time.Millisecond,
		Fill: func(_ context.Context, missing []string, _ map[string]Recipe) (Set, error) {
			if fills.Add(1) < 3 {
				panic("a fault in the fill")
			}
			return Set{Pictures: map[string][]byte{"bed/red": picture(t, 16, 16, blue)}}, nil
		}}
	m.Run(t.Context())
	if fills.Load() != 3 {
		t.Errorf("filled %d times, want it tried until it stopped going wrong", fills.Load())
	}
	if _, ok := m.Picture("container/chest"); !ok {
		t.Error("what was held was lost to the fault")
	}
	if _, ok := m.Picture("bed/red"); !ok {
		t.Error("what was missing never arrived")
	}

	var fetches atomic.Int64
	fresh := &Mobs{Dir: t.TempDir(), Ref: testRef, Logger: quiet(), RetryMin: time.Millisecond, RetryMax: 2 * time.Millisecond, Fetch: func(context.Context) (Set, error) {
		if fetches.Add(1) < 3 {
			panic("a fault in the fetch")
		}
		return Set{Mobs: map[string][]byte{"cow": picture(t, 16, 16, red)}}, nil
	}}
	fresh.Run(t.Context())
	if _, ok := fresh.Icon("cow"); !ok || fetches.Load() != 3 {
		t.Errorf("after two faults the fetch was tried %d times and the icon is served: %v", fetches.Load(), ok)
	}
}

// truncating is the samples with the whole-tree listing cut short, as the
// host cuts one over 100,000 entries or 7 MB, and counts the listings.
func truncating(t *testing.T) (*samples, *atomic.Int64, *atomic.Int64) {
	t.Helper()
	s := newSamples(t)
	withMobs(t, s)
	s.truncated = true
	var wide, narrow atomic.Int64
	inner := s.srv.Config.Handler
	s.srv.Config.Handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasPrefix(r.URL.Path, "/list/"):
			wide.Add(1)
		case strings.HasPrefix(r.URL.Path, "/dir/"):
			narrow.Add(1)
		}
		inner.ServeHTTP(w, r)
	})
	return s, &wide, &narrow
}

func TestWhenTheWholeTreeCannotComeWholeEverythingButTheFacesIsStillFetched(t *testing.T) {
	s, wide, narrow := truncating(t)
	source := s.source()
	source.DirURL = s.srv.URL + "/dir"
	source.State = filepath.Join(t.TempDir(), ListingState)
	set, err := source.Fetch(t.Context())
	if err != nil {
		t.Fatalf("with the tree cut short the fetch failed outright: %v", err)
	}
	// Everything that was fetched before any picture was made from a
	// model, and the blocks, which need no listing.
	if len(set.Mobs) != 5 || len(set.Lang) == 0 {
		t.Errorf("%d mob icons and %d names", len(set.Mobs), len(set.Lang))
	}
	for _, key := range append(pictureKeys(), "block/chest", "structure/fortress") {
		if _, ok := set.Pictures[key]; !ok {
			t.Errorf("no %s", key)
		}
	}
	if _, made := set.Pictures["face/blaze"]; made {
		t.Error("a face was made with no model listed")
	}
	// Said once as lasting, and not asked for again.
	if set.Rejected[artPlan] == "" || len(set.Missing) != 0 {
		t.Errorf("rejected %q, missing %v: it would be asked for again every hour", set.Rejected[artPlan], set.Missing)
	}
	if wide.Load() != 1 || narrow.Load() != 1 {
		t.Errorf("%d whole and %d narrow listings, want one of each", wide.Load(), narrow.Load())
	}
	// The next asking at this pin does not spend a listing on the tree
	// that is known not to come whole.
	hold(t, source.State, time.Now().Add(-time.Minute))
	if _, err := source.Fetch(t.Context()); err != nil {
		t.Fatal(err)
	}
	if wide.Load() != 1 || narrow.Load() != 2 {
		t.Errorf("after that, %d whole and %d narrow listings", wide.Load(), narrow.Load())
	}
	// Without the narrow listing to fall back on, it fails as it did.
	plain := s.source()
	if _, err := plain.Fetch(t.Context()); !errors.Is(err, errListingTooLong) {
		t.Errorf("with no fallback: %v", err)
	}
}

// hold rewrites the record of the last listing so that the next may be
// asked for at next, keeping the rest of it.
func hold(t *testing.T, state string, next time.Time) {
	t.Helper()
	var g listingGate
	raw, err := os.ReadFile(state)
	if err != nil || json.Unmarshal(raw, &g) != nil {
		t.Fatalf("no record of the last listing: %v", err)
	}
	g.Next = next
	raw, _ = json.Marshal(g)
	if err := os.WriteFile(state, raw, 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestTheListingIsNotAskedForAgainBeforeItsTurnAcrossRestarts(t *testing.T) {
	s := newSamples(t)
	var listings atomic.Int64
	var refuse atomic.Bool
	reset := time.Now().Add(40 * time.Minute)
	inner := s.srv.Config.Handler
	s.srv.Config.Handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/list/") {
			listings.Add(1)
			if refuse.Load() {
				w.Header().Set("X-RateLimit-Remaining", "0")
				w.Header().Set("X-RateLimit-Reset", strconv.FormatInt(reset.Unix(), 10))
				http.Error(w, "API rate limit exceeded", http.StatusForbidden)
				return
			}
		}
		inner.ServeHTTP(w, r)
	})
	state := filepath.Join(t.TempDir(), ListingState)
	fresh := func() *Source {
		// A new one each time, as after a restart: only the file is kept.
		source := s.source()
		source.State = state
		return source
	}
	if _, err := fresh().Fetch(t.Context()); err != nil {
		t.Fatal(err)
	}
	for range 5 {
		if _, err := fresh().Fetch(t.Context()); err == nil {
			t.Fatal("a fetch moments after the last listed again")
		}
	}
	if listings.Load() != 1 {
		t.Fatalf("%d listings in six starts moments apart, want one", listings.Load())
	}
	var kept listingGate
	raw, _ := os.ReadFile(state)
	if json.Unmarshal(raw, &kept) != nil || time.Until(kept.Next) < listingGap-time.Minute {
		t.Errorf("after an answer the next listing is due %s from now", time.Until(kept.Next))
	}
	// Its turn come, it is asked; refused with the host's word for when
	// its ration is given again, it waits until then and not a minute.
	hold(t, state, time.Now().Add(-time.Second))
	refuse.Store(true)
	if _, err := fresh().Fetch(t.Context()); err == nil {
		t.Fatal("a refused listing was taken as an answer")
	}
	raw, _ = os.ReadFile(state)
	if json.Unmarshal(raw, &kept) != nil || kept.Next.Unix() != reset.Unix() || kept.Failures != 1 {
		t.Errorf("after a refusal the next listing is due %s, want the host's %s", kept.Next, reset)
	}
	if _, err := fresh().Fetch(t.Context()); err == nil || listings.Load() != 2 {
		t.Errorf("%d listings: one was asked for before the host said it could be", listings.Load())
	}
}

func TestTheWaitAfterAListingGrowsAndIsBounded(t *testing.T) {
	now := time.Now()
	failed := errors.New("no route")
	g := listingGate{}
	var waits []time.Duration
	for range 9 {
		g = g.after(now, http.Header{}, failed)
		waits = append(waits, g.Next.Sub(now))
	}
	if waits[0] != defaultRetryMin || waits[1] != 2*defaultRetryMin || waits[8] != defaultRetryMax {
		t.Errorf("waits after failures = %v", waits)
	}
	// In any hour of nothing but failures: at 0, 1, 3, 7, 15 and 31
	// minutes, and the next at 63.
	asked, at := 0, time.Duration(0)
	for _, wait := range waits {
		if at < time.Hour {
			asked++
		}
		at += wait
	}
	if asked != 6 {
		t.Errorf("%d listings in an hour of failures, want 6", asked)
	}
	if g = g.after(now, http.Header{}, nil); g.Failures != 0 || g.Next.Sub(now) != listingGap {
		t.Errorf("after an answer: %+v", g)
	}
	// What the host says is kept to, within reason.
	header := http.Header{}
	header.Set("Retry-After", "600")
	if g = (listingGate{}).after(now, header, failed); g.Next.Sub(now) != 10*time.Minute {
		t.Errorf("told to wait ten minutes, waits %s", g.Next.Sub(now))
	}
	header = http.Header{}
	header.Set("X-RateLimit-Remaining", "0")
	header.Set("X-RateLimit-Reset", strconv.FormatInt(now.Add(72*time.Hour).Unix(), 10))
	if g = (listingGate{}).after(now, header, failed); g.Next.Sub(now) != maxListingWait {
		t.Errorf("told to wait three days, waits %s", g.Next.Sub(now))
	}
	// A record from a clock that was wrong holds nothing up for long.
	state := filepath.Join(t.TempDir(), ListingState)
	raw, _ := json.Marshal(listingGate{Next: now.Add(1000 * time.Hour)})
	_ = os.WriteFile(state, raw, 0o644)
	if got := (&Source{State: state}).readGate(); time.Until(got.Next) > maxListingWait+time.Minute {
		t.Errorf("a record far in the future holds the listing for %s", time.Until(got.Next))
	}
}

func TestTheListingIsGivenLongerToArriveThanAnythingElse(t *testing.T) {
	s := newSamples(t)
	inner := s.srv.Config.Handler
	s.srv.Config.Handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/list/") {
			time.Sleep(300 * time.Millisecond)
		}
		inner.ServeHTTP(w, r)
	})
	source := s.source()
	source.HTTP.Timeout = 100 * time.Millisecond
	if _, err := source.Fetch(t.Context()); err != nil {
		t.Errorf("a listing slower than a file is allowed to be was given up on: %v", err)
	}
}

func TestTheRecordOfTheLastListingOutlivesAChangeOfPin(t *testing.T) {
	dir := t.TempDir()
	state := filepath.Join(dir, ListingState)
	if err := os.WriteFile(state, []byte(`{"next":"2020-01-01T00:00:00Z"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, ref := range []string{"v1", "v2"} {
		m := &Mobs{Dir: dir, Ref: ref, Logger: quiet(), Fetch: func(context.Context) (Set, error) {
			return Set{Mobs: map[string][]byte{"cow": picture(t, 16, 16, red)}}, nil
		}}
		m.Run(t.Context())
	}
	if _, err := os.Stat(state); err != nil {
		t.Errorf("storing a pin's set removed the record of the last listing: %v", err)
	}
}

func TestASetOverTheLimitsIsNotWrittenToBeRefusedAtEveryStart(t *testing.T) {
	dir := t.TempDir()
	good := &Mobs{Dir: dir, Ref: testRef, Logger: quiet(), Fetch: func(context.Context) (Set, error) {
		return Set{Mobs: map[string][]byte{"cow": picture(t, 16, 16, red)}}, nil
	}}
	good.Run(t.Context())
	tiny := picture(t, 1, 1, red)
	over := Set{Mobs: map[string][]byte{"cow": picture(t, 16, 16, blue)}, Pictures: map[string][]byte{}}
	for n := range maxPictures + 1 {
		over.Pictures[fmt.Sprintf("face/m%d", n)] = tiny
	}
	m := &Mobs{Dir: dir, Ref: testRef, Logger: quiet()}
	if err := m.store(over); err == nil {
		t.Fatal("a set with more pictures than an index is read back with was stored")
	}
	recipes := Set{Mobs: over.Mobs, Recipes: map[string]Recipe{}}
	for n := range maxRecipes + 1 {
		recipes.Recipes[fmt.Sprintf("face/m%d", n)] = Recipe{Flat: "textures/items/x"}
	}
	if err := m.store(recipes); err == nil {
		t.Fatal("a set with more recipes than an index is read back with was stored")
	}
	// What was there is still there, and still reads.
	kept, _, err := (&Mobs{Dir: dir, Ref: testRef}).load()
	if err != nil || colourOf(t, kept.Mobs["cow"]) != red {
		t.Errorf("the set that was held was lost to one that could not be kept: %v", err)
	}
	// And the plan itself stops short of the limit.
	models := modelsOf(t, olderModels)
	controllers, _ := parseControllers(t.Context(), []byte(syntheticControllers))
	looks := map[string]mobLook{}
	for n := range maxRecipes + 50 {
		looks[fmt.Sprintf("m%d", n)] = mobLook{egg: true, appearance: appearance{Textures: map[string]string{"default": "textures/entity/x"},
			Geometry: map[string]string{"default": "geometry.walker"}, Controllers: []json.RawMessage{json.RawMessage(`"controller.render.plain"`)}}}
	}
	made, _ := plan(t.Context(), library{models: models, controllers: controllers}, looks, atlas{}, atlas{}, nil)
	if len(made) > maxRecipes {
		t.Errorf("%d recipes planned, over the %d an index is read back with", len(made), maxRecipes)
	}
}

func TestAReasonIsKeptAsOneLineOfWholeCharacters(t *testing.T) {
	long := strings.Repeat("é", maxRejection)
	got := tidyReason(long)
	if len(got) > maxRejection || !strings.HasSuffix(got, "é") || strings.ContainsRune(got, '�') {
		t.Errorf("a reason of two-byte letters was cut to %d bytes ending %q", len(got), got[len(got)-3:])
	}
	if got := tidyReason("no model is named geometry.x\n\x1b[31mERROR‮ fake line"); strings.ContainsAny(got, "\n\x1b‮") {
		t.Errorf("a reason kept what would break a log line: %q", got)
	}
	if got := tidyReason("bad \xff\xfe bytes"); !strings.HasPrefix(got, "bad ") || strings.Contains(got, "\xff") {
		t.Errorf("a reason kept bytes that are no text: %q", got)
	}
	// And that is what is kept and served.
	dir := t.TempDir()
	m := &Mobs{Dir: dir, Ref: testRef, Logger: quiet(), Fetch: func(context.Context) (Set, error) {
		return Set{Mobs: map[string][]byte{"cow": picture(t, 16, 16, red)}, Rejected: map[string]string{"face/cow": long + "\nsecond line"}}, nil
	}}
	m.Run(t.Context())
	if why := m.Rejected()["face/cow"]; len(why) > maxRejection || strings.Contains(why, "\n") {
		t.Errorf("served a reason of %d bytes", len(why))
	}
	if kept, _, err := (&Mobs{Dir: dir, Ref: testRef}).load(); err != nil || kept.Rejected["face/cow"] == "" {
		t.Errorf("a long reason made the index unreadable: %v", err)
	}
}

func TestADefinitionsDepthIsCountedAfterItsCommentsAreGone(t *testing.T) {
	s := newSamples(t)
	deep := strings.Repeat("[", maxJSONDepth+4) + strings.Repeat("]", maxJSONDepth+4)
	// The quote in the comment, read as the start of a string, turns every
	// string after it inside out, and the nesting is counted as text.
	s.set("resource_pack/entity/deep.entity.json", []byte(`// a "quote, left open in a comment
{"minecraft:client_entity":{"description":{"identifier":"minecraft:deepmob","deep":`+deep+`,"spawn_egg":{"texture":"spawn_egg_cow"}}}}`))
	s.set("resource_pack/entity/shallow.entity.json", []byte(`// a "quote, left open in a comment
{"minecraft:client_entity":{"description":{"identifier":"minecraft:shallowmob","spawn_egg":{"texture":"spawn_egg_cow"}}}}`))
	set, err := s.source().Fetch(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if slices.Contains(set.Entities, "deepmob") {
		t.Error("a definition nested past the limit was read because a comment hid it")
	}
	if !slices.Contains(set.Entities, "shallowmob") {
		t.Error("a definition with a quote in its comment was not read at all")
	}
}

// An index that cannot be trusted is not served from; the set is fetched
// again, and served.
func TestAnIndexThatIsNotTrustedIsReplacedByAFetch(t *testing.T) {
	dir := t.TempDir()
	bad := &Mobs{Dir: dir, Ref: testRef, Logger: quiet(), Fetch: func(context.Context) (Set, error) {
		return Set{Mobs: map[string][]byte{"cow": picture(t, 16, 16, red)}, Recipes: map[string]Recipe{"face/cow": {Block: &Block{W: 1000, H: 16, D: 16}}}}, nil
	}}
	bad.Run(t.Context())
	var fetched atomic.Int64
	m := &Mobs{Dir: dir, Ref: testRef, Logger: quiet(), Fetch: func(context.Context) (Set, error) {
		fetched.Add(1)
		return Set{Mobs: map[string][]byte{"cow": picture(t, 16, 16, blue)}}, nil
	}}
	m.Run(t.Context())
	if raw, ok := m.Icon("cow"); fetched.Load() != 1 || !ok || colourOf(t, raw) != blue {
		t.Errorf("fetched %d times; the icon served is from the index that was not trusted: %v", fetched.Load(), ok)
	}
}
