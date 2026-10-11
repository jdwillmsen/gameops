package icons

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"image/png"
	"log/slog"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"
	"unicode"
	"unicode/utf8"
)

const (
	indexFile = "index.json"
	// ListingState is the file, beside the pins' directories, that the
	// source keeps its record of the last listing in.
	ListingState = "listing.json"
	// indexFormat is raised when a fetch starts reading something more, so
	// that a volume filled by a version that read less is fetched again
	// and not taken as complete.
	indexFormat = 2

	// How long to leave the source alone after it fails. The first wait is
	// short because the usual failure is a network not ready yet at start;
	// the last is long because the unauthenticated listing is rationed by
	// address, and asking more often only spends the ration.
	defaultRetryMin = time.Minute
	defaultRetryMax = time.Hour

	// How long to leave alone what the source answered that it does not
	// hold. A wrong answer of that kind is rare and a pin's contents do
	// not change, so asking is cheap but seldom worth it.
	defaultRefillEvery = 24 * time.Hour

	// maxRejection is the longest reason kept for a picture not made.
	maxRejection = 300
)

// Mobs holds the mob icons for one pinned revision and, fetched with them,
// the marker pictures and the display names. It starts empty, which is
// what the page draws dots and rings for and names by tidied ids, and
// fills once all of it has been read from the volume or, failing that,
// fetched. Nothing waits on it.
type Mobs struct {
	// Dir is where what was fetched is kept, one directory per pin.
	Dir string
	Ref string
	// Fetch downloads everything; Source.Fetch outside tests.
	Fetch func(context.Context) (Set, error)
	// Fill asks again for what a set is missing and nothing else;
	// Source.Fill outside tests. Nil leaves a set as it was first fetched.
	// recipes is how each picture made here is made, as far as is known.
	Fill   func(ctx context.Context, missing []string, recipes map[string]Recipe) (Set, error)
	Logger *slog.Logger

	// RetryMin and RetryMax bound the wait between failed fetches. Zero
	// uses the defaults.
	RetryMin, RetryMax time.Duration
	// RefillEvery is the wait before asking again for what the source
	// said it does not hold. Zero uses the default.
	RefillEvery time.Duration

	mu      sync.RWMutex
	icons   map[string][]byte
	types   []string
	version string

	pictures       map[string][]byte
	pictureKeys    []string
	pictureVersion string
	heads          map[string][4]int
	names          *Names
	rejected       map[string]string
}

type index struct {
	Format int    `json:"format"`
	Ref    string `json:"ref"`
	// Icons maps a mob type to the SHA-256 of its file, which is also the
	// file's name: several types share one texture.
	Icons map[string]string `json:"icons"`
	// Pictures maps a marker picture's key to its file in the same way.
	Pictures map[string]string `json:"pictures"`
	Lang     map[string]string `json:"lang"`
	Entities []string          `json:"entities"`
	Missing  []string          `json:"missing"`
	// Structures is the kinds of structure whose pictures the source was
	// asked for. An index without it was written when there were five.
	Structures []string `json:"structures,omitempty"`
	// Recipes and Rejected are of the pictures made here, and Art the
	// revision of the making they came from. An index without them was
	// written before any picture was made.
	Recipes  map[string]Recipe `json:"recipes,omitempty"`
	Rejected map[string]string `json:"rejected,omitempty"`
	Art      int               `json:"art,omitempty"`
}

// firstStructureKinds is the kinds an index that names none was written
// for.
var firstStructureKinds = []string{"fortress", "monument", "outpost", "witch_hut", "village"}

// home is the directory for this pin. The pin is hashed because it is a
// setting and may hold characters a path should not.
func (m *Mobs) home() string {
	sum := sha256.Sum256([]byte(m.Ref))
	return filepath.Join(m.Dir, hex.EncodeToString(sum[:8]))
}

// Run fills the set and returns when it is full or ctx ends. What is on the
// volume for this pin is used if every file of it is intact, so a restart
// asks the source for nothing it holds; otherwise the source is asked
// until it answers, with a growing wait in between. Whatever a set is
// then missing is asked for again, by itself, for as long as it is.
func (m *Mobs) Run(ctx context.Context) {
	set, earlier, err := m.load()
	if err == nil {
		if earlier {
			// Written again as this version writes it, so that what is
			// fetched for it below has an index to be added to.
			m.keep(set)
			m.Logger.Info("mob icons on the volume are from before there were marker pictures and names; those are fetched now", "ref", m.Ref, "types", len(set.Mobs))
		} else {
			m.set(set)
			m.Logger.Info("mob icons read from the volume", "ref", m.Ref, "types", len(set.Mobs), "pictures", len(set.Pictures), "names", len(set.Lang), "missing", set.Missing)
		}
		// At once: what was missing when this last ran may only have
		// been missing then.
		m.refill(ctx, set, 0)
		return
	}
	wait, ceiling := m.retries()
	for {
		set, err := m.fetch(ctx)
		if err == nil {
			metricFetches.WithLabelValues(resultOK).Inc()
			m.keep(set)
			if why := set.Rejected[artPlan]; why != "" {
				// Said once: nothing will change it but another pin.
				m.Logger.Warn("mob faces are not made at this pin; mobs are drawn as their spawn eggs", "ref", m.Ref, "why", why)
			}
			m.Logger.Info("mob icons fetched", "ref", m.Ref, "types", len(set.Mobs), "pictures", len(set.Pictures), "names", len(set.Lang), "not_made", len(set.Rejected), "missing", set.Missing)
			again := m.refillEvery()
			if len(set.Unreached) > 0 {
				again = wait
			}
			m.refill(ctx, set, again)
			return
		}
		if ctx.Err() != nil {
			return
		}
		metricFetches.WithLabelValues(resultFailed).Inc()
		m.Logger.Warn("mob icons not fetched; the map draws dots and rings, and names by tidied ids, until they are", "ref", m.Ref, "retry_in", wait.String(), "error", err.Error())
		select {
		case <-ctx.Done():
			return
		case <-time.After(wait):
		}
		wait = min(wait*2, ceiling)
	}
}

func (m *Mobs) retries() (first, ceiling time.Duration) {
	first, ceiling = m.RetryMin, m.RetryMax
	if first <= 0 {
		first = defaultRetryMin
	}
	if ceiling <= 0 {
		ceiling = defaultRetryMax
	}
	return first, ceiling
}

func (m *Mobs) refillEvery() time.Duration {
	if m.RefillEvery <= 0 {
		return defaultRefillEvery
	}
	return m.RefillEvery
}

// fetch and fill are Fetch and Fill with a fault in either taken as its
// failing: what is held goes on being served, and it is tried again as
// anything else that failed is.
func (m *Mobs) fetch(ctx context.Context) (set Set, err error) {
	defer func() {
		if fault := recover(); fault != nil {
			metricFaults.Inc()
			set, err = Set{}, fmt.Errorf("the fetch stopped on a fault: %s", tidyReason(fmt.Sprint(fault)))
		}
	}()
	return m.Fetch(ctx)
}

func (m *Mobs) fill(ctx context.Context, held Set) (set Set, err error) {
	defer func() {
		if fault := recover(); fault != nil {
			metricFaults.Inc()
			m.Logger.Error("asking for what the mob icons are missing stopped on a fault; what is held is still served", "fault", tidyReason(fmt.Sprint(fault)))
			set, err = Set{}, errors.New("a fault")
		}
	}()
	return m.Fill(ctx, held.Missing, held.Recipes)
}

// keep serves a set and writes it to the volume.
func (m *Mobs) keep(set Set) {
	if len(set.Rejected) > 0 {
		tidied := make(map[string]string, len(set.Rejected))
		for _, key := range slices.Sorted(maps.Keys(set.Rejected))[:min(len(set.Rejected), maxRecipes)] {
			tidied[key] = tidyReason(set.Rejected[key])
		}
		set.Rejected = tidied
	}
	if err := m.store(set); err != nil {
		// Still served from memory; the next start fetches again.
		m.Logger.Warn("mob icons not kept on the volume", "error", err.Error())
	}
	m.set(set)
}

// refill asks, after wait, for what a set is missing, and goes on asking
// until nothing is. What the set holds is served throughout. One answer
// that a file is absent, too large or not a picture is not taken as the
// last word, since a source can give a wrong one: it is asked again every
// so often. Being unable to ask at all is tried again sooner, with the
// same growing wait as a first fetch.
//
// It is quiet about nothing having changed: a gap that stays a gap is
// logged when it was first found and not once for every time it is asked
// after, and a source that stays out of reach is logged once.
func (m *Mobs) refill(ctx context.Context, set Set, wait time.Duration) {
	if m.Fill == nil {
		return
	}
	first, ceiling := m.retries()
	backoff, failing := first, false
	for len(set.Missing) > 0 {
		select {
		case <-ctx.Done():
			return
		case <-time.After(wait):
		}
		got, err := m.fill(ctx, set)
		if ctx.Err() != nil {
			return
		}
		if err != nil {
			got = Set{Missing: set.Missing, Unreached: set.Missing}
		}
		if len(got.Pictures) > 0 || len(got.Lang) > 0 || len(got.Rejected) > 0 || got.Replanned {
			// A new set, not the held one changed: that one is being
			// read by whoever is serving from it.
			next := set
			next.Pictures = maps.Clone(set.Pictures)
			if next.Pictures == nil {
				next.Pictures = map[string][]byte{}
			}
			maps.Copy(next.Pictures, got.Pictures)
			if got.Replanned {
				next.Recipes, next.Rejected = got.Recipes, got.Rejected
			} else if len(got.Rejected) > 0 {
				next.Rejected = maps.Clone(set.Rejected)
				if next.Rejected == nil {
					next.Rejected = map[string]string{}
				}
				maps.Copy(next.Rejected, got.Rejected)
			}
			// One made under an earlier revision and not wanted under
			// this is not left standing.
			for key := range got.Rejected {
				delete(next.Pictures, key)
			}
			if len(got.Lang) > 0 {
				next.Lang = got.Lang
			}
			next.Missing, next.Unreached = got.Missing, nil
			set = next
			m.keep(set)
			m.Logger.Info("fetched what the mob icons were missing", "ref", m.Ref, "pictures", len(set.Pictures), "names", len(set.Lang), "not_made", len(set.Rejected), "missing", set.Missing)
		}
		if len(got.Unreached) == 0 {
			metricFetches.WithLabelValues(resultOK).Inc()
			wait, backoff, failing = m.refillEvery(), first, false
			continue
		}
		metricFetches.WithLabelValues(resultFailed).Inc()
		if !failing {
			m.Logger.Warn("what the mob icons are missing could not be asked for; it is tried again", "ref", m.Ref, "missing", set.Missing)
		}
		wait, backoff, failing = backoff, min(backoff*2, ceiling), true
	}
}

// tidyReason is why a picture was not made, fit to keep and to log: one
// line of printable text no longer than maxRejection, cut between
// characters and not through one. A reason can carry a name out of a file
// that came from outside.
func tidyReason(why string) string {
	var b strings.Builder
	for _, r := range strings.ToValidUTF8(why, "?") {
		if unicode.IsControl(r) || unicode.In(r, unicode.Cf, unicode.Co, unicode.Zl, unicode.Zp) {
			r = ' '
		}
		if b.Len()+utf8.RuneLen(r) > maxRejection {
			break
		}
		b.WriteRune(r)
	}
	return b.String()
}

// versionOf changes when the pin or any picture in the set does.
func (m *Mobs) versionOf(set map[string][]byte) (string, []string) {
	keys := slices.Sorted(maps.Keys(set))
	sum := sha256.New()
	sum.Write([]byte(m.Ref))
	for _, key := range keys {
		fmt.Fprintf(sum, "\x00%s\x00", key)
		sum.Write(set[key])
	}
	return hex.EncodeToString(sum.Sum(nil)[:8]), keys
}

func (m *Mobs) set(set Set) {
	version, types := m.versionOf(set.Mobs)
	pictureVersion, pictureKeys := m.versionOf(set.Pictures)
	names := NewNames(set.Lang, set.Entities)
	m.mu.Lock()
	m.icons, m.types, m.version = set.Mobs, types, version
	m.pictures, m.pictureKeys, m.pictureVersion = set.Pictures, pictureKeys, pictureVersion
	m.heads = headBoxes(set.Pictures, set.Recipes)
	m.names = names
	m.rejected = set.Rejected
	m.mu.Unlock()
	metricMobTypes.Set(float64(len(types)))
	metricPictures.Set(float64(len(pictureKeys)))
	metricNames.Set(float64(names.Translated()))
}

// Icon is the PNG for a mob type, if there is one.
func (m *Mobs) Icon(kind string) ([]byte, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	png, ok := m.icons[kind]
	return png, ok
}

// Listing is every type that has an icon, and a version that changes when
// the pin or any icon does. Both are empty until the set has filled.
func (m *Mobs) Listing() (version string, types []string) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.version, m.types
}

// Picture is the PNG for a marker or structure picture, by its key, if
// there is one.
func (m *Mobs) Picture(key string) ([]byte, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	png, ok := m.pictures[key]
	return png, ok
}

// Pictures is the key of every marker and structure picture there is, and
// a version that changes when the pin or any of them does. Both are empty
// until the set has filled.
func (m *Mobs) Pictures() (version string, keys []string) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	if len(m.pictureKeys) == 0 {
		return "", nil
	}
	return m.pictureVersion, m.pictureKeys
}

// headBoxes is where the head is in each picture that is a face, by the
// picture's key. A page centres a face on its plate by the head, and
// sizes it by the head, so that a villager's nose or a witch's hat does
// not push the face off the middle or make it smaller than the next.
func headBoxes(pictures map[string][]byte, recipes map[string]Recipe) map[string][4]int {
	out := map[string][4]int{}
	for key, recipe := range recipes {
		raw, held := pictures[key]
		if !held {
			continue
		}
		// A structure's picture is its mob's face, or the item that stands
		// in while the face cannot be made, and only the bytes say which.
		if recipe.Else != "" {
			kind := strings.TrimPrefix(key, "structure/")
			if face, made := pictures[faceKey(structureFaces[kind])]; !made || !bytes.Equal(raw, face) {
				continue
			}
		}
		cfg, err := png.DecodeConfig(bytes.NewReader(raw))
		if err != nil || cfg.Width != cfg.Height {
			continue
		}
		if box, ok := recipe.head(cfg.Width); ok {
			out[key] = box
		}
	}
	return out
}

// Heads is where the head is in each picture that is a face, by the
// picture's key, as x, y, width and height in the picture's own pixels.
// The caller must not change it.
func (m *Mobs) Heads() map[string][4]int {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.heads
}

// Rejected is every picture that could have been made from the models and
// was not, by key, with why. The page draws each as it would with no such
// picture: a mob as its spawn egg.
func (m *Mobs) Rejected() map[string]string {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.rejected
}

// Names is the display names. Before the set has filled, and for as long
// as the language file is out of reach, they are tidied ids.
func (m *Mobs) Names() *Names {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.names
}

// load reads this pin's set from the volume. earlier reports one written
// before there were marker pictures and names: its mob icons are as whole
// as they ever were and are used as they are, with every picture and the
// language file counted as missing, so that a version that wants more
// than the volume holds never serves less than the last one did while it
// waits for the source.
func (m *Mobs) load() (set Set, earlier bool, err error) {
	home := m.home()
	raw, err := os.ReadFile(filepath.Join(home, indexFile))
	if err != nil {
		return Set{}, false, err
	}
	var idx index
	if err := json.Unmarshal(raw, &idx); err != nil {
		return Set{}, false, err
	}
	// That version wrote no format, and nothing but the icons.
	if earlier = idx.Format == 0 && len(idx.Pictures) == 0 && len(idx.Lang) == 0 && len(idx.Entities) == 0; earlier {
		idx.Format = indexFormat
		idx.Missing = append(pictureKeys(), langPath)
		slices.Sort(idx.Missing)
		// The types with an icon are the ones it is known to define.
		idx.Entities = slices.Sorted(maps.Keys(idx.Icons))
	}
	if idx.Format != indexFormat || idx.Ref != m.Ref || len(idx.Icons) == 0 || len(idx.Icons) > maxDefinitions ||
		len(idx.Pictures) > maxPictures || len(idx.Lang) > maxLangNames || len(idx.Entities) > maxDefinitions || len(idx.Missing) > maxPictures+2 ||
		len(idx.Recipes) > maxRecipes || len(idx.Rejected) > maxRecipes {
		return Set{}, false, errors.New("the icon index is not for this pin")
	}
	files := map[string][]byte{}
	read := func(digest string) ([]byte, error) {
		if !isDigest(digest) {
			return nil, errors.New("the icon index is damaged")
		}
		if body, seen := files[digest]; seen {
			return body, nil
		}
		body, err := os.ReadFile(filepath.Join(home, digest+".png"))
		if err != nil {
			return nil, err
		}
		if sum := sha256.Sum256(body); hex.EncodeToString(sum[:]) != digest {
			return nil, fmt.Errorf("icon %s on the volume is damaged", digest)
		}
		files[digest] = body
		return body, nil
	}
	out := Set{Mobs: make(map[string][]byte, len(idx.Icons)), Pictures: make(map[string][]byte, len(idx.Pictures)), Entities: idx.Entities, Missing: idx.Missing}
	for kind, digest := range idx.Icons {
		if !mobType.MatchString(kind) {
			return Set{}, false, errors.New("the icon index is damaged")
		}
		if out.Mobs[kind], err = read(digest); err != nil {
			return Set{}, false, err
		}
	}
	for key, digest := range idx.Pictures {
		if !pictureKey.MatchString(key) {
			return Set{}, false, errors.New("the icon index is damaged")
		}
		if out.Pictures[key], err = read(digest); err != nil {
			return Set{}, false, err
		}
	}
	// The names are held to what the language file was, since they go to
	// a browser as they are and nothing vouches for the index but its
	// place on the volume.
	for key, name := range idx.Lang {
		if clean, ok := cleanLangName(name); !ok || clean != name || !wantedLangKey(key) {
			return Set{}, false, errors.New("the icon index is damaged")
		}
	}
	out.Lang = idx.Lang
	// A version that marks a kind of structure the one that wrote the
	// index did not, wants a picture the source was never asked for. It is
	// missing like any other, and is asked for like any other.
	asked := idx.Structures
	if len(asked) == 0 {
		asked = firstStructureKinds
	}
	for _, kind := range StructureKinds {
		if key := "structure/" + kind; !slices.Contains(asked, kind) && !slices.Contains(out.Missing, key) {
			out.Missing = append(slices.Clip(out.Missing), key)
			slices.Sort(out.Missing)
		}
	}
	if slices.ContainsFunc(idx.Entities, func(kind string) bool { return !mobType.MatchString(kind) }) {
		return Set{}, false, errors.New("the icon index is damaged")
	}
	for key, recipe := range idx.Recipes {
		if !pictureKey.MatchString(key) || recipe.check() != nil {
			return Set{}, false, errors.New("the icon index is damaged")
		}
	}
	for key, why := range idx.Rejected {
		if !pictureKey.MatchString(key) || len(why) > maxRejection {
			return Set{}, false, errors.New("the icon index is damaged")
		}
	}
	// What was made under another revision, or before anything was, is
	// served as it is and made again: the recipes are worked out afresh,
	// so none of the old ones is kept to make a picture the old way.
	if idx.Art == ArtRevision {
		out.Recipes, out.Rejected = idx.Recipes, idx.Rejected
	} else if !slices.Contains(out.Missing, artPlan) {
		out.Missing = append(slices.Clip(out.Missing), artPlan)
		slices.Sort(out.Missing)
	}
	return out, earlier, nil
}

func isDigest(s string) bool {
	raw, err := hex.DecodeString(s)
	return err == nil && len(raw) == sha256.Size
}

// store writes the set beside where it will live and renames it into place,
// so a start never finds half of one, then removes what other pins left.
func (m *Mobs) store(set Set) error {
	if err := os.MkdirAll(m.Dir, 0o755); err != nil {
		return err
	}
	staging, err := os.MkdirTemp(m.Dir, "install-*")
	if err != nil {
		return err
	}
	defer os.RemoveAll(staging)
	idx := index{Format: indexFormat, Ref: m.Ref, Icons: map[string]string{}, Pictures: map[string]string{}, Lang: set.Lang, Entities: set.Entities, Missing: set.Missing, Structures: StructureKinds, Recipes: set.Recipes, Rejected: set.Rejected, Art: ArtRevision}
	for _, group := range []struct {
		from map[string][]byte
		to   map[string]string
	}{{set.Mobs, idx.Icons}, {set.Pictures, idx.Pictures}} {
		for key, body := range group.from {
			sum := sha256.Sum256(body)
			digest := hex.EncodeToString(sum[:])
			group.to[key] = digest
			if err := os.WriteFile(filepath.Join(staging, digest+".png"), body, 0o644); err != nil {
				return err
			}
		}
	}
	// What load would refuse is not written: a set over a limit would be
	// kept, refused at the next start and fetched again, every start.
	if len(set.Mobs) > maxDefinitions || len(set.Pictures) > maxPictures || len(set.Recipes) > maxRecipes || len(set.Rejected) > maxRecipes ||
		len(set.Missing) > maxPictures+2 || len(set.Entities) > maxDefinitions || len(set.Lang) > maxLangNames {
		return fmt.Errorf("the set is over the limits an index is read back within: %d pictures, %d recipes, %d not made", len(set.Pictures), len(set.Recipes), len(set.Rejected))
	}
	raw, err := json.Marshal(idx)
	if err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(staging, indexFile), raw, 0o644); err != nil {
		return err
	}
	home := m.home()
	if err := os.RemoveAll(home); err != nil {
		return err
	}
	if err := os.Rename(staging, home); err != nil {
		return err
	}
	others, _ := os.ReadDir(m.Dir)
	for _, other := range others {
		if other.Name() != filepath.Base(home) && other.Name() != ListingState {
			_ = os.RemoveAll(filepath.Join(m.Dir, other.Name()))
		}
	}
	return nil
}
