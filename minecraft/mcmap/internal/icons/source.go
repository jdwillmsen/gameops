package icons

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"maps"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

// Where the samples are published. The listing comes from the API host and
// every file from the raw host; nothing is asked of anywhere else.
const (
	DefaultListURL = "https://api.github.com/repos/Mojang/bedrock-samples/git/trees"
	DefaultRawURL  = "https://raw.githubusercontent.com/Mojang/bedrock-samples"
	DefaultDirURL  = "https://api.github.com/repos/Mojang/bedrock-samples/contents"

	packDir   = "resource_pack"
	entityDir = "resource_pack/entity"
	atlasPath = "resource_pack/textures/item_texture.json"
)

// Bounds on one fetch. The real set is a listing of 5.5 MB, about 180
// definitions of 30 KB at most, 370 models and render controllers of 13 KB
// at most, 330 textures of which the spawn eggs are under 2 KB each and
// the largest model's is 41 KB, and a language file of 0.8 MB; these are
// several times that, and are here so a source that has gone wrong costs a
// bounded amount of memory and time whatever it sends.
const (
	maxListBytes       = 16 << 20
	maxListEntries     = 100_000
	maxAtlasBytes      = 1 << 20
	maxDefinitionBytes = 256 << 10
	maxTextureBytes    = 64 << 10
	maxDefinitions     = 600
	maxTextures        = 400
	maxTotalBytes      = 40 << 20

	// A spawn-egg texture is 16 pixels a side. Larger is allowed for, since
	// a resource pack may be drawn at a higher resolution, but not much.
	maxIconSide = 64

	requestTimeout = 15 * time.Second
	// The listing is some three hundred times the size of anything else
	// asked for, and is given that much longer to arrive.
	listTimeout  = 2 * time.Minute
	fetchTimeout = 3 * time.Minute
	fetchWorkers = 6
)

// Source reads the mob icons, the marker pictures and the display names at
// one pinned revision of the samples.
type Source struct {
	// Ref is the tag or commit every request names.
	Ref     string
	ListURL string
	RawURL  string
	// HTTP is replaced in tests. Nil is a client that follows no redirect.
	HTTP *http.Client
	// Logger is where a fault in making a picture is said. Nil is the
	// default logger.
	Logger *slog.Logger
	// DirURL lists one directory to a request, and is what the entity
	// definitions are listed with when the whole tree cannot come whole.
	// Empty is no such fallback.
	DirURL string
	// State is a file on the volume that holds when the listing was last
	// asked for and how that went, so that a service restarting over and
	// over does not spend the listing's ration. Empty keeps no such
	// record, and nothing is held back.
	State string

	// listed is the last listing this run was given, and listedAt the pin
	// it was of.
	mu       sync.Mutex
	listed   listing
	listedAt string
}

// NewClient is the client the samples are fetched with. Neither host
// redirects a request for a file at a named revision, so a redirect is an
// answer from something else and is not followed anywhere.
func NewClient() *http.Client {
	return &http.Client{
		Timeout:       requestTimeout,
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}
}

// errSettled marks an answer from the source that it does not hold what
// was asked for: the pin has no such file, or has one too large to be it.
// It is told apart from getting no answer because the two are asked for
// again on different schedules. The mob icons treat it as any other
// failure.
var errSettled = errors.New("the samples do not hold it at this pin")

// Set is everything one fetch read.
type Set struct {
	// Mobs is each mob type's icon, by type without the minecraft: prefix.
	Mobs map[string][]byte
	// Pictures is each marker and structure picture, by its key.
	Pictures map[string][]byte
	// Lang is the display names the language file gave, by its own keys.
	Lang map[string]string
	// Entities is every entity type the samples define.
	Entities []string
	// Missing is every marker picture, by key, and the language file, by
	// its path, that this set does not hold.
	Missing []string
	// Unreached is those of Missing the source could not be asked for
	// this time, as opposed to asked and found not to hold.
	Unreached []string
	// Recipes is how each picture made here, and not served as it came,
	// is made, by the picture's key.
	Recipes map[string]Recipe
	// Rejected is every such picture that was not made though the source
	// answered, by key, with why: a mob with no head to take a face from,
	// a face that came out blank.
	Rejected map[string]string
	// Replanned marks a set whose Recipes and Rejected were worked out
	// afresh and are the whole of them, not an addition.
	Replanned bool
}

// budget is how much one fetch may still download.
type budget struct {
	mu   sync.Mutex
	left int64
}

func (b *budget) take(n int64) bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	if n > b.left {
		return false
	}
	b.left -= n
	return true
}

func (s *Source) get(ctx context.Context, address string, limit int64, total *budget) ([]byte, error) {
	body, _, err := s.ask(ctx, s.client(), address, limit, total)
	return body, err
}

func (s *Source) client() *http.Client {
	if s.HTTP != nil {
		return s.HTTP
	}
	return NewClient()
}

// errTooLarge marks an answer longer than this would read.
var errTooLarge = errors.New("more than is read")

// ask is get with the client to ask with, and gives back the headers of
// whatever answer came, which say when a rationed host may be asked again.
func (s *Source) ask(ctx context.Context, client *http.Client, address string, limit int64, total *budget) ([]byte, http.Header, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, address, nil)
	if err != nil {
		return nil, nil, err
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusNotFound {
		return nil, resp.Header, fmt.Errorf("%s: %s: %w", req.URL.Path, resp.Status, errSettled)
	}
	if resp.StatusCode != http.StatusOK {
		return nil, resp.Header, fmt.Errorf("%s: %s", req.URL.Path, resp.Status)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, limit+1))
	if err != nil {
		return nil, resp.Header, err
	}
	if int64(len(body)) > limit {
		return nil, resp.Header, fmt.Errorf("%s: over the %d byte limit: %w: %w", req.URL.Path, limit, errTooLarge, errSettled)
	}
	if !total.take(int64(len(body))) {
		return nil, resp.Header, errors.New("the icon source sent more than a whole fetch is allowed")
	}
	return body, resp.Header, nil
}

func (s *Source) raw(path string) string {
	parts := strings.Split(path, "/")
	for i, part := range parts {
		parts[i] = url.PathEscape(part)
	}
	return strings.TrimRight(s.RawURL, "/") + "/" + url.PathEscape(s.Ref) + "/" + strings.Join(parts, "/")
}

var (
	// One of the samples' own files has a space before its extension.
	definitionName = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._ -]{0,120}\.json$`)
	texturePath    = regexp.MustCompile(`^textures/items/[a-z0-9_]+(/[a-z0-9_]+)*$`)
	mobType        = regexp.MustCompile(`^[a-z0-9_]{1,64}$`)
)

// definition is the part of a client entity definition that says which
// item texture its spawn egg is drawn with.
type definition struct {
	Entity struct {
		Description struct {
			Identifier string `json:"identifier"`
			MinEngine  string `json:"min_engine_version"`
			SpawnEgg   *struct {
				Texture string `json:"texture"`
				Index   int    `json:"texture_index"`
			} `json:"spawn_egg"`
			appearance
		} `json:"description"`
	} `json:"minecraft:client_entity"`
}

type atlas struct {
	Textures map[string]struct {
		// One path, or a list of them that a definition indexes into.
		Textures json.RawMessage `json:"textures"`
	} `json:"texture_data"`
}

// path is the texture an atlas entry names at index, or "" if there is no
// such entry or it is not a path this service would ask for.
func (a atlas) path(name string, index int) string {
	return a.pathIn(name, index, texturePath)
}

// pathIn is path for an atlas whose textures are kept somewhere else.
func (a atlas) pathIn(name string, index int, allowed *regexp.Regexp) string {
	entry, ok := a.Textures[name]
	if !ok {
		return ""
	}
	var one string
	var many []json.RawMessage
	switch {
	case json.Unmarshal(entry.Textures, &one) == nil:
		if index != 0 {
			return ""
		}
	case json.Unmarshal(entry.Textures, &many) == nil:
		if index < 0 || index >= len(many) || json.Unmarshal(many[index], &one) != nil {
			return ""
		}
	default:
		return ""
	}
	if !allowed.MatchString(one) {
		return ""
	}
	return one
}

// Fetch downloads the icon of every mob type the samples define one for,
// the marker pictures and the display names. Which texture belongs to
// which mob is read from the samples themselves: each client entity
// definition names its spawn egg's entry in the item texture atlas, and the
// atlas names the file. The names do not follow from the type (an
// evocation_illager's egg is the evoker's, and a villager's is one of sixty
// in a shared list), so none of them is guessed.
//
// The mob icons come whole or the fetch fails. A marker picture or the
// language file never fails it: one the pin does not hold, or holds in a
// form this refuses, or that the source could not be asked for, is left
// out and named in Missing, to be asked for again later by Fill.
func (s *Source) Fetch(ctx context.Context) (Set, error) {
	ctx, cancel := context.WithTimeout(ctx, fetchTimeout)
	defer cancel()
	total := &budget{left: maxTotalBytes}

	ls, err := s.list(ctx, total)
	if err != nil {
		return Set{}, fmt.Errorf("listing the samples: %w", err)
	}
	rawAtlas, err := s.get(ctx, s.raw(atlasPath), maxAtlasBytes, total)
	if err != nil {
		return Set{}, fmt.Errorf("reading the item texture atlas: %w", err)
	}
	var items atlas
	if err := json.Unmarshal(stripComments(rawAtlas), &items); err != nil {
		return Set{}, fmt.Errorf("reading the item texture atlas: %w", err)
	}
	chosen, err := s.definitions(ctx, ls.definitions, items, total)
	if err != nil {
		return Set{}, err
	}

	kindsOf := map[string][]string{}
	var textures []string
	for kind, c := range chosen {
		if c.texture == "" {
			continue
		}
		if _, seen := kindsOf[c.texture]; !seen {
			textures = append(textures, c.texture)
		}
		kindsOf[c.texture] = append(kindsOf[c.texture], kind)
	}
	if len(textures) == 0 {
		return Set{}, errors.New("no entity definition names a spawn egg texture; the samples are not laid out as expected")
	}
	if len(textures) > maxTextures {
		return Set{}, fmt.Errorf("the samples name %d spawn egg textures, over the limit of %d", len(textures), maxTextures)
	}
	images, err := each(ctx, textures, func(ctx context.Context, path string) ([]byte, error) {
		body, err := s.get(ctx, s.raw("resource_pack/"+path+".png"), maxTextureBytes, total)
		if err != nil {
			return nil, err
		}
		clean, err := Clean(body, 1, maxIconSide, false)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", path, err)
		}
		return clean, nil
	})
	if err != nil {
		return Set{}, fmt.Errorf("reading spawn egg textures: %w", err)
	}
	out := Set{Mobs: map[string][]byte{}, Entities: slices.Sorted(maps.Keys(chosen))}
	for path, kinds := range kindsOf {
		for _, kind := range kinds {
			out.Mobs[kind] = images[path]
		}
	}

	want := append(pictureKeys(), langPath)
	// Whatever becomes of these, the mob icons are already whole and are
	// not thrown away for them.
	extra := s.extras(ctx, want, items, total)
	out.Pictures, out.Lang, out.Missing, out.Unreached = extra.Pictures, extra.Lang, extra.Missing, extra.Unreached
	looks := map[string]mobLook{}
	for kind, c := range chosen {
		looks[kind] = c.look
	}
	out.add(s.art(ctx, ls, looks, items, total))
	return out, nil
}

// add puts what another part of a fetch read into the set.
func (set *Set) add(more Set) {
	if set.Pictures == nil {
		set.Pictures = map[string][]byte{}
	}
	maps.Copy(set.Pictures, more.Pictures)
	set.Missing = append(set.Missing, more.Missing...)
	set.Unreached = append(set.Unreached, more.Unreached...)
	slices.Sort(set.Missing)
	slices.Sort(set.Unreached)
	if more.Recipes != nil {
		set.Recipes = more.Recipes
	}
	if more.Rejected != nil {
		set.Rejected = more.Rejected
	}
	set.Replanned = set.Replanned || more.Replanned
}

// choice is the definition of a mob type that the game uses: the newest of
// however many the samples hold.
type choice struct {
	version []int
	// texture is its spawn egg's, or empty for one with none this would
	// ask for.
	texture string
	look    mobLook
}

// definitions reads every client entity definition listed and keeps, for
// each type, the newest. It comes whole or fails.
func (s *Source) definitions(ctx context.Context, names []string, items atlas, total *budget) (map[string]choice, error) {
	bodies, err := each(ctx, names, func(ctx context.Context, name string) ([]byte, error) {
		return s.get(ctx, s.raw(entityDir+"/"+name), maxDefinitionBytes, total)
	})
	if err != nil {
		return nil, fmt.Errorf("reading entity definitions: %w", err)
	}
	// A mob can have several definitions, one per engine version it changed
	// in; the game uses the newest, and so does this.
	chosen := map[string]choice{}
	for _, name := range names {
		var def definition
		// Comments first: a quote inside one would hide whatever nesting
		// follows it from the count.
		plain := stripComments(bodies[name])
		if !jsonWithin(plain, maxJSONDepth) || json.Unmarshal(plain, &def) != nil {
			continue
		}
		d := def.Entity.Description
		kind, ok := strings.CutPrefix(d.Identifier, "minecraft:")
		if !ok || !mobType.MatchString(kind) {
			continue
		}
		version := engineVersion(d.MinEngine)
		if held, seen := chosen[kind]; seen && !newer(version, held.version) {
			continue
		}
		texture := ""
		if d.SpawnEgg != nil {
			texture = items.path(d.SpawnEgg.Texture, d.SpawnEgg.Index)
		}
		chosen[kind] = choice{version, texture, mobLook{appearance: d.appearance, egg: d.SpawnEgg != nil}}
	}
	return chosen, nil
}

// Fill asks again for what an earlier fetch left out, and for nothing else:
// no listing, and the atlas only if a bed is among them, since that is
// where a bed's path is written. A picture made from a recipe is made again
// from the recipe held for it, which asks only for its textures; only where
// the recipes themselves are missing are the models read again, and the
// listing with them. It returns what it got, with what is still left out in
// Missing.
func (s *Source) Fill(ctx context.Context, missing []string, recipes map[string]Recipe) (Set, error) {
	ctx, cancel := context.WithTimeout(ctx, fetchTimeout)
	defer cancel()
	total := &budget{left: maxTotalBytes}
	plain, made := split(missing)
	var out Set
	if len(plain) > 0 {
		var items atlas
		reached := true
		if slices.ContainsFunc(plain, func(what string) bool { return strings.HasPrefix(what, "bed/") }) {
			raw, err := s.get(ctx, s.raw(atlasPath), maxAtlasBytes, total)
			switch {
			case err == nil:
				// An atlas that does not parse lists no bed, which leaves
				// each bed out as it would any the atlas did not name.
				_ = json.Unmarshal(stripComments(raw), &items)
			case !errors.Is(err, errSettled):
				reached = false
			}
		}
		if !reached {
			return Set{Missing: missing, Unreached: missing}, nil
		}
		out = s.extras(ctx, plain, items, total)
	}
	switch {
	case len(made) == 0:
	case len(plain) > 0 && len(out.Unreached) == len(plain):
		// The source is out of reach, and the rationed listing is not
		// spent on finding that out again.
		out.add(Set{Missing: made, Unreached: made})
	default:
		out.add(s.remake(ctx, made, recipes, total))
	}
	return out, ctx.Err()
}

// extras reads the marker pictures and the language file named in want.
// Each comes out one of three ways: fetched; left out because the source
// answered and did not have it in a usable form, which is in Missing; or
// left out because the source could not be asked, which is in Missing and
// in Unreached. Nothing here is an error, since none of it stops the map.
func (s *Source) extras(ctx context.Context, want []string, items atlas, total *budget) Set {
	out := Set{Pictures: map[string][]byte{}}
	known := map[string]markerPicture{}
	for _, p := range pictures(items) {
		known[p.name] = p
	}
	var (
		mu sync.Mutex
		// down is set by the first request that gets no answer, so that
		// a source that is out of reach is found so once and not once
		// for every picture.
		down atomic.Bool
	)
	settled := func(what string) {
		mu.Lock()
		defer mu.Unlock()
		out.Missing = append(out.Missing, what)
	}
	unreached := func(what string) {
		down.Store(true)
		mu.Lock()
		defer mu.Unlock()
		out.Missing = append(out.Missing, what)
		out.Unreached = append(out.Unreached, what)
	}
	var keys []string
	wantLang := false
	for _, what := range want {
		switch _, picture := known[what]; {
		case what == langPath:
			wantLang = true
		case picture:
			keys = append(keys, what)
		default:
			// A bed the atlas does not list, which has no path to ask
			// for, or a key that is no picture's.
			settled(what)
		}
	}
	got, err := each(ctx, keys, func(ctx context.Context, key string) ([]byte, error) {
		if down.Load() {
			unreached(key)
			return nil, nil
		}
		p := known[key]
		body, err := s.get(ctx, s.raw("resource_pack/"+p.path+".png"), maxTextureBytes, total)
		if err == nil {
			if p.sheet {
				body, err = shulkerIcon(body)
			} else {
				body, err = Clean(body, 1, maxIconSide, false)
			}
			// A file that is there and is not a small picture stays so.
			if err != nil {
				err = fmt.Errorf("%s: %w: %w", p.path, err, errSettled)
			}
		}
		switch {
		case errors.Is(err, errSettled):
			settled(key)
			return nil, nil
		case err != nil:
			unreached(key)
			return nil, nil
		}
		return body, nil
	})
	if err != nil {
		// Only the context ending stops the pictures part way.
		return Set{Missing: want, Unreached: want}
	}
	for key, body := range got {
		if body != nil {
			out.Pictures[key] = body
		}
	}
	if wantLang {
		var raw []byte
		err := errors.New("the source is out of reach")
		if !down.Load() {
			if raw, err = s.get(ctx, s.raw(langPath), maxLangBytes, total); err == nil {
				if out.Lang, err = parseLang(raw); err != nil {
					err = fmt.Errorf("%w: %w", err, errSettled)
				}
			}
		}
		switch {
		case errors.Is(err, errSettled):
			settled(langPath)
		case err != nil:
			unreached(langPath)
		}
	}
	slices.Sort(out.Missing)
	slices.Sort(out.Unreached)
	return out
}

// listing is the files of the samples this reads whole directories of:
// the name of each entity definition, and the path under resource_pack of
// each model and render controller.
type listing struct {
	definitions, models, controllers []string
	// partial is why the models and controllers are not listed, where
	// only the definitions could be: no face can be worked out then.
	partial string
	// tga is the textures, by path without an extension, kept as a TGA
	// with no PNG beside it.
	tga map[string]bool
}

// errListingTooLong marks a listing of the whole tree that cannot come
// whole at this pin, however often it is asked for: the host cuts a tree
// short at 100,000 entries or 7 MB, and the one at the default pin is
// 18,714 entries and 5.5 MB.
var errListingTooLong = errors.New("the listing of the samples is too long to come whole")

// list is the files of the samples that whole directories are read of. It
// asks for the whole of resource_pack in one request, and where that
// cannot come whole, for the entity definitions alone, as it did before
// any picture was made from a model: the icons, the names and the pictures
// served as they come need nothing more, and only the faces go without.
//
// The host rations listings by address, sixty an hour, so each asking and
// how it went is written to the volume, and it is not asked again, by this
// run or by the next after a restart, before its turn.
func (s *Source) list(ctx context.Context, total *budget) (listing, error) {
	gate := s.readGate()
	if wait := time.Until(gate.Next); wait > 0 {
		// The listing this run was already given costs no request to use
		// again: a fetch that got it and then failed on a file is tried
		// again in a minute with it, and not held up for the listing's
		// sake. A restart has none, and waits.
		s.mu.Lock()
		held, ref := s.listed, s.listedAt
		s.mu.Unlock()
		if ref == s.Ref && len(held.definitions) > 0 {
			return held, nil
		}
		return listing{}, fmt.Errorf("the listing was asked for a short while ago and is not asked for again for %s", wait.Round(time.Second))
	}
	var (
		out     listing
		headers http.Header
		err     error
	)
	if gate.Narrow != s.Ref {
		if out, headers, err = s.wide(ctx, total); errors.Is(err, errListingTooLong) && s.DirURL != "" {
			gate.Narrow = s.Ref
		}
	}
	if gate.Narrow == s.Ref {
		if out, headers, err = s.narrow(ctx, total); err == nil {
			out.partial = errListingTooLong.Error() + " at this pin; faces are not made until the pin changes"
		}
	}
	s.writeGate(gate.after(time.Now(), headers, err))
	if err == nil {
		s.mu.Lock()
		s.listed, s.listedAt = out, s.Ref
		s.mu.Unlock()
	}
	return out, err
}

// How long the listing is left alone after it was asked for: after an
// answer, long enough that a service restarting every few seconds asks six
// times an hour and not six hundred; after a failure, a minute, then
// doubling to an hour. The host's own word for when it may be asked again
// is kept to where it is longer, up to maxListingWait.
const (
	listingGap     = 10 * time.Minute
	maxListingWait = 2 * time.Hour
)

// listingGate is what is kept of the last asking.
type listingGate struct {
	Next     time.Time `json:"next"`
	Failures int       `json:"failures,omitempty"`
	// Narrow is the pin whose whole tree is known not to come whole.
	Narrow string `json:"narrow,omitempty"`
}

func (g listingGate) after(now time.Time, headers http.Header, err error) listingGate {
	if err == nil {
		g.Failures, g.Next = 0, now.Add(listingGap)
		return g
	}
	g.Failures = min(g.Failures+1, 16)
	g.Next = now.Add(min(defaultRetryMin<<(g.Failures-1), defaultRetryMax))
	// The host says when a ration that is spent is given again, as a time
	// or as a number of seconds.
	var told time.Time
	if headers.Get("X-RateLimit-Remaining") == "0" {
		if at, err := strconv.ParseInt(headers.Get("X-RateLimit-Reset"), 10, 64); err == nil {
			told = time.Unix(at, 0)
		}
	}
	if seconds, err := strconv.Atoi(headers.Get("Retry-After")); err == nil && seconds > 0 {
		told = now.Add(time.Duration(min(seconds, int(maxListingWait/time.Second))) * time.Second)
	}
	if told.After(g.Next) {
		g.Next = told
	}
	if latest := now.Add(maxListingWait); g.Next.After(latest) {
		g.Next = latest
	}
	return g
}

func (s *Source) readGate() listingGate {
	var g listingGate
	if s.State == "" {
		return g
	}
	if raw, err := os.ReadFile(s.State); err == nil && len(raw) < 4096 {
		_ = json.Unmarshal(raw, &g)
	}
	// A record from a clock that was wrong does not hold things up for
	// longer than any real one could.
	if latest := time.Now().Add(maxListingWait); g.Next.After(latest) {
		g.Next = latest
	}
	return g
}

func (s *Source) writeGate(g listingGate) {
	if s.State == "" {
		return
	}
	raw, _ := json.Marshal(g)
	if err := os.MkdirAll(filepath.Dir(s.State), 0o755); err == nil {
		// Not kept is asked sooner, which is the state before this was
		// kept at all.
		_ = os.WriteFile(s.State, raw, 0o644)
	}
}

// narrow lists the entity definitions and nothing else, in one request.
func (s *Source) narrow(ctx context.Context, total *budget) (listing, http.Header, error) {
	address := strings.TrimRight(s.DirURL, "/") + "/" + entityDir + "?ref=" + url.QueryEscape(s.Ref)
	body, headers, err := s.ask(ctx, s.client(), address, maxListBytes, total)
	if err != nil {
		return listing{}, headers, err
	}
	var entries []struct {
		Name string `json:"name"`
		Type string `json:"type"`
	}
	if err := json.Unmarshal(body, &entries); err != nil {
		return listing{}, headers, err
	}
	var out listing
	for _, e := range entries {
		if e.Type == "file" && definitionName.MatchString(e.Name) {
			out.definitions = append(out.definitions, e.Name)
		}
	}
	if len(out.definitions) == 0 {
		return listing{}, headers, errors.New("the listing holds no definitions")
	}
	if len(out.definitions) > maxDefinitions {
		return listing{}, headers, fmt.Errorf("the listing holds %d definitions, over the limit of %d", len(out.definitions), maxDefinitions)
	}
	return out, headers, nil
}

// wide asks, in one request, for every file under resource_pack. Three
// directories are wanted and the host lists one directory to a request,
// or a whole tree; the tree is twenty times the size and a third of the
// requests.
func (s *Source) wide(ctx context.Context, total *budget) (listing, http.Header, error) {
	address := strings.TrimRight(s.ListURL, "/") + "/" + url.PathEscape(s.Ref) + "%3A" + packDir + "?recursive=1"
	client := *s.client()
	client.Timeout = listTimeout
	body, headers, err := s.ask(ctx, &client, address, maxListBytes, total)
	if errors.Is(err, errTooLarge) {
		return listing{}, headers, fmt.Errorf("%w: %w", errListingTooLong, err)
	}
	if err != nil {
		return listing{}, headers, err
	}
	var tree struct {
		Entries []struct {
			Path string `json:"path"`
			Type string `json:"type"`
		} `json:"tree"`
		Truncated bool `json:"truncated"`
	}
	if err := json.Unmarshal(body, &tree); err != nil {
		return listing{}, headers, err
	}
	// A listing cut short would leave mobs out without a word.
	if tree.Truncated || len(tree.Entries) > maxListEntries {
		return listing{}, headers, errListingTooLong
	}
	out := listing{tga: map[string]bool{}}
	png := map[string]bool{}
	for _, e := range tree.Entries {
		if e.Type != "blob" {
			continue
		}
		if texture, ok := strings.CutSuffix(e.Path, ".png"); ok {
			png[texture] = true
		} else if texture, ok := strings.CutSuffix(e.Path, tgaSuffix); ok && strings.HasPrefix(texture, "textures/entity/") {
			out.tga[texture] = true
		}
		dir, name := "", e.Path
		if i := strings.LastIndexByte(e.Path, '/'); i >= 0 {
			dir, name = e.Path[:i+1], e.Path[i+1:]
		}
		if !definitionName.MatchString(name) {
			continue
		}
		switch {
		case dir == "entity/":
			out.definitions = append(out.definitions, name)
		case dir == modelDir || e.Path == legacyModels:
			out.models = append(out.models, e.Path)
		case dir == controllerDir:
			out.controllers = append(out.controllers, e.Path)
		}
	}
	if len(out.definitions) == 0 {
		return listing{}, headers, errors.New("the listing holds no definitions")
	}
	if len(out.definitions) > maxDefinitions {
		return listing{}, headers, fmt.Errorf("the listing holds %d definitions, over the limit of %d", len(out.definitions), maxDefinitions)
	}
	maps.DeleteFunc(out.tga, func(texture string, _ bool) bool { return png[texture] })
	// Too many of either is a listing of something else; the faces go
	// without and the icons are not held up for them.
	if len(out.models) > maxModelFiles || len(out.controllers) > maxControllerFiles {
		out.models, out.controllers = nil, nil
	}
	return out, headers, nil
}

// each runs get for every key, a few at a time, and stops at the first
// failure: half a set of icons is not kept, so there is no use finishing it.
func each(ctx context.Context, keys []string, get func(context.Context, string) ([]byte, error)) (map[string][]byte, error) {
	ctx, cancel := context.WithCancelCause(ctx)
	defer cancel(nil)
	var (
		mu  sync.Mutex
		wg  sync.WaitGroup
		out = make(map[string][]byte, len(keys))
	)
	work := make(chan string)
	for range min(fetchWorkers, len(keys)) {
		wg.Go(func() {
			for key := range work {
				body, err := get(ctx, key)
				if err != nil {
					cancel(err)
					continue
				}
				mu.Lock()
				out[key] = body
				mu.Unlock()
			}
		})
	}
feed:
	for _, key := range keys {
		select {
		case work <- key:
		case <-ctx.Done():
			break feed
		}
	}
	close(work)
	wg.Wait()
	if err := context.Cause(ctx); err != nil {
		return nil, err
	}
	return out, nil
}

func engineVersion(s string) []int {
	var out []int
	for part := range strings.SplitSeq(s, ".") {
		n, err := strconv.Atoi(part)
		if err != nil {
			return nil
		}
		out = append(out, n)
	}
	return out
}

func newer(a, b []int) bool {
	for i := range max(len(a), len(b)) {
		var x, y int
		if i < len(a) {
			x = a[i]
		}
		if i < len(b) {
			y = b[i]
		}
		if x != y {
			return x > y
		}
	}
	return false
}

// stripComments removes // and /* */ comments, which the samples' JSON
// carries and encoding/json refuses. Text inside a string is left alone.
func stripComments(in []byte) []byte {
	out := make([]byte, 0, len(in))
	for i := 0; i < len(in); i++ {
		c := in[i]
		switch {
		case c == '"':
			start := i
			for i++; i < len(in) && in[i] != '"'; i++ {
				if in[i] == '\\' {
					i++
				}
			}
			out = append(out, in[start:min(i+1, len(in))]...)
		case c == '/' && i+1 < len(in) && in[i+1] == '/':
			for i < len(in) && in[i] != '\n' {
				i++
			}
			out = append(out, '\n')
		case c == '/' && i+1 < len(in) && in[i+1] == '*':
			for i += 2; i+1 < len(in) && (in[i] != '*' || in[i+1] != '/'); i++ {
			}
			i++
		default:
			out = append(out, c)
		}
	}
	return out
}
