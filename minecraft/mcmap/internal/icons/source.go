package icons

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"sync"
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
// most and 130 textures under 2 KB each; these are several times that, and
// are here so a source that has gone wrong costs a bounded amount of memory
// and time whatever it sends.
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

// Source reads the mob icons at one pinned revision of the samples.
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
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("%s: %s", req.URL.Path, resp.Status)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, limit+1))
	if err != nil {
		return nil, err
	}
	if int64(len(body)) > limit {
		return nil, fmt.Errorf("%s: over the %d byte limit", req.URL.Path, limit)
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
// keyed by type without the minecraft: prefix. Which texture belongs to
// which mob is read from the samples themselves: each client entity
// definition names its spawn egg's entry in the item texture atlas, and the
// atlas names the file. The names do not follow from the type (an
// evocation_illager's egg is the evoker's, and a villager's is one of sixty
// in a shared list), so none of them is guessed.
func (s *Source) Fetch(ctx context.Context) (map[string][]byte, error) {
	ctx, cancel := context.WithTimeout(ctx, fetchTimeout)
	defer cancel()
	total := &budget{left: maxTotalBytes}

	names, err := s.list(ctx, total)
	if err != nil {
		return nil, fmt.Errorf("listing entity definitions: %w", err)
	}
	rawAtlas, err := s.get(ctx, s.raw(atlasPath), maxAtlasBytes, total)
	if err != nil {
		return nil, fmt.Errorf("reading the item texture atlas: %w", err)
	}
	var items atlas
	if err := json.Unmarshal(stripComments(rawAtlas), &items); err != nil {
		return nil, fmt.Errorf("reading the item texture atlas: %w", err)
	}

	bodies, err := each(ctx, names, func(ctx context.Context, name string) ([]byte, error) {
		return s.get(ctx, s.raw(entityDir+"/"+name), maxDefinitionBytes, total)
	})
	if err != nil {
		return nil, fmt.Errorf("reading entity definitions: %w", err)
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
		return nil, errors.New("no entity definition names a spawn egg texture; the samples are not laid out as expected")
	}
	if len(textures) > maxTextures {
		return nil, fmt.Errorf("the samples name %d spawn egg textures, over the limit of %d", len(textures), maxTextures)
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
		return nil, fmt.Errorf("reading spawn egg textures: %w", err)
	}
	out := map[string][]byte{}
	for path, kinds := range kindsOf {
		for _, kind := range kinds {
			out[kind] = images[path]
		}
	}
	return out, nil
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
