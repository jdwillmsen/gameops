package icons

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"maps"
	"net/http"
	"net/url"
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
	DefaultListURL = "https://api.github.com/repos/Mojang/bedrock-samples/contents"
	DefaultRawURL  = "https://raw.githubusercontent.com/Mojang/bedrock-samples"

	entityDir = "resource_pack/entity"
	atlasPath = "resource_pack/textures/item_texture.json"
)

// Bounds on one fetch. The real set is about 180 definitions of 30 KB at
// most, 130 textures under 2 KB each and a language file of 0.8 MB; these
// are several times that, and are here so a source that has gone wrong
// costs a bounded amount of memory and time whatever it sends.
const (
	maxListBytes       = 2 << 20
	maxAtlasBytes      = 1 << 20
	maxDefinitionBytes = 256 << 10
	maxTextureBytes    = 64 << 10
	maxDefinitions     = 600
	maxTextures        = 400
	maxTotalBytes      = 16 << 20

	// A spawn-egg texture is 16 pixels a side. Larger is allowed for, since
	// a resource pack may be drawn at a higher resolution, but not much.
	maxIconSide = 64

	requestTimeout = 15 * time.Second
	fetchTimeout   = 3 * time.Minute
	fetchWorkers   = 6
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

// errSettled marks an answer that asking again would not change: the pin
// has no such file, or has one too large to be what was asked for. The mob
// icons treat it as any other failure. A marker picture or the language
// file is left out for it instead, so that a pin lacking one of them still
// yields everything else.
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
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, address, nil)
	if err != nil {
		return nil, err
	}
	client := s.HTTP
	if client == nil {
		client = NewClient()
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusNotFound {
		return nil, fmt.Errorf("%s: %s: %w", req.URL.Path, resp.Status, errSettled)
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("%s: %s", req.URL.Path, resp.Status)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, limit+1))
	if err != nil {
		return nil, err
	}
	if int64(len(body)) > limit {
		return nil, fmt.Errorf("%s: over the %d byte limit: %w", req.URL.Path, limit, errSettled)
	}
	if !total.take(int64(len(body))) {
		return nil, errors.New("the icon source sent more than a whole fetch is allowed")
	}
	return body, nil
}

func (s *Source) raw(path string) string {
	return strings.TrimRight(s.RawURL, "/") + "/" + url.PathEscape(s.Ref) + "/" + path
}

var (
	definitionName = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,120}\.json$`)
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
	if !texturePath.MatchString(one) {
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
// language file that the pin does not hold, or holds in a form this
// refuses, is left out and named in Missing, to be asked for again later;
// only a failure to reach the source at all fails the fetch for them.
func (s *Source) Fetch(ctx context.Context) (Set, error) {
	ctx, cancel := context.WithTimeout(ctx, fetchTimeout)
	defer cancel()
	total := &budget{left: maxTotalBytes}

	names, err := s.list(ctx, total)
	if err != nil {
		return Set{}, fmt.Errorf("listing entity definitions: %w", err)
	}
	rawAtlas, err := s.get(ctx, s.raw(atlasPath), maxAtlasBytes, total)
	if err != nil {
		return Set{}, fmt.Errorf("reading the item texture atlas: %w", err)
	}
	var items atlas
	if err := json.Unmarshal(stripComments(rawAtlas), &items); err != nil {
		return Set{}, fmt.Errorf("reading the item texture atlas: %w", err)
	}

	bodies, err := each(ctx, names, func(ctx context.Context, name string) ([]byte, error) {
		return s.get(ctx, s.raw(entityDir+"/"+name), maxDefinitionBytes, total)
	})
	if err != nil {
		return Set{}, fmt.Errorf("reading entity definitions: %w", err)
	}

	// A mob can have several definitions, one per engine version it changed
	// in; the game uses the newest, and so does this.
	type choice struct {
		version []int
		texture string
	}
	chosen := map[string]choice{}
	for _, name := range names {
		var def definition
		if json.Unmarshal(stripComments(bodies[name]), &def) != nil {
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
		chosen[kind] = choice{version, texture}
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
	extra := s.extras(ctx, want, items, total)
	if len(extra.Unreached) > 0 {
		return Set{}, fmt.Errorf("reading marker pictures and the language file: %d of %d could not be asked for", len(extra.Unreached), len(want))
	}
	out.Pictures, out.Lang, out.Missing = extra.Pictures, extra.Lang, extra.Missing
	return out, nil
}

// Fill asks again for what an earlier fetch left out, and for nothing else:
// no listing, and the atlas only if a bed is among them, since that is
// where a bed's path is written. It returns what it got, with what is
// still left out in Missing.
func (s *Source) Fill(ctx context.Context, missing []string) (Set, error) {
	ctx, cancel := context.WithTimeout(ctx, fetchTimeout)
	defer cancel()
	total := &budget{left: maxTotalBytes}
	var items atlas
	if slices.ContainsFunc(missing, func(what string) bool { return strings.HasPrefix(what, "bed/") }) {
		raw, err := s.get(ctx, s.raw(atlasPath), maxAtlasBytes, total)
		switch {
		case err == nil:
			// An atlas that does not parse lists no bed, which leaves
			// each bed out as it would any the atlas did not name.
			_ = json.Unmarshal(stripComments(raw), &items)
		case !errors.Is(err, errSettled):
			return Set{Missing: missing, Unreached: missing}, nil
		}
	}
	return s.extras(ctx, missing, items, total), ctx.Err()
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

func (s *Source) list(ctx context.Context, total *budget) ([]string, error) {
	address := strings.TrimRight(s.ListURL, "/") + "/" + entityDir + "?ref=" + url.QueryEscape(s.Ref)
	body, err := s.get(ctx, address, maxListBytes, total)
	if err != nil {
		return nil, err
	}
	var entries []struct {
		Name string `json:"name"`
		Type string `json:"type"`
	}
	if err := json.Unmarshal(body, &entries); err != nil {
		return nil, err
	}
	var names []string
	for _, e := range entries {
		if e.Type == "file" && definitionName.MatchString(e.Name) {
			names = append(names, e.Name)
		}
	}
	if len(names) == 0 {
		return nil, errors.New("the listing holds no definitions")
	}
	if len(names) > maxDefinitions {
		return nil, fmt.Errorf("the listing holds %d definitions, over the limit of %d", len(names), maxDefinitions)
	}
	return names, nil
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
