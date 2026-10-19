package atlas

import (
	"compress/gzip"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

// DefaultBaseURL is the GitHub REST API root.
const DefaultBaseURL = "https://api.github.com"

// DefaultRawBaseURL serves file contents. Unlike the REST API it is not
// subject to the 60 requests per hour anonymous limit, which is why the
// per-texture downloads go through it.
const DefaultRawBaseURL = "https://raw.githubusercontent.com"

const (
	repoPath     = "/repos/Mojang/bedrock-samples"
	rawRepoPath  = "/Mojang/bedrock-samples"
	packPrefix   = "resource_pack/"
	completeMark = ".complete"
	// cacheLayout is written into the complete mark. It is raised when the
	// fetch starts reading more, so that a directory filled by an older
	// version is fetched again and not taken as complete.
	cacheLayout = "2"
	indexFile   = "index.json"

	defaultBackoff        = 2 * time.Second
	defaultRequestTimeout = 60 * time.Second
	fileWorkers           = 16
	apiAccept             = "application/vnd.github+json"
	rawAccept             = "*/*"
)

// Pack is a resource pack on local disk.
type Pack struct {
	Tag string
	// Dir is the resource_pack directory.
	Dir string
	// Bytes, Files and Requests describe the download; all are zero when
	// served from the cache. Bytes counts what crossed the wire, before
	// decompression.
	Bytes    int64
	Files    int
	Requests int
	Elapsed  time.Duration
}

// Fetcher downloads the parts of the vanilla resource pack that Build reads.
// The zero value uses the public GitHub endpoints; tests inject a client and
// base URL.
type Fetcher struct {
	Client  *http.Client
	BaseURL string
	// RawBaseURL serves file contents. Empty means DefaultRawBaseURL, or
	// BaseURL+"/raw" when BaseURL is overridden.
	RawBaseURL string
	// Backoff is the wait before the single retry of a failed request.
	Backoff time.Duration
	// OnDownload, if set, is called after a download completes, so callers
	// can log its size and duration.
	OnDownload func(Pack)
}

// Fetch downloads the needed resource pack files for the release tag nearest
// below serverVersion into cacheDir/<tag>/. Repeat calls for a version already
// fetched make no network requests.
func Fetch(ctx context.Context, serverVersion, cacheDir string) (Pack, error) {
	return Fetcher{}.Fetch(ctx, serverVersion, cacheDir)
}

func (f Fetcher) Fetch(ctx context.Context, serverVersion, cacheDir string) (Pack, error) {
	// The server version to tag mapping is remembered because resolving it
	// needs the tag list, which would otherwise break offline restarts.
	if tag, ok := readIndex(cacheDir)[serverVersion]; ok && cached(cacheDir, tag) {
		return Pack{Tag: tag, Dir: packDir(cacheDir, tag)}, nil
	}
	start := time.Now()
	s := &session{Fetcher: f}
	tags, err := s.listTags(ctx)
	if err != nil {
		return Pack{}, err
	}
	tag, err := SelectTag(serverVersion, tags)
	if err != nil {
		return Pack{}, err
	}
	pack := Pack{Tag: tag, Dir: packDir(cacheDir, tag)}
	if !cached(cacheDir, tag) {
		if pack.Files, err = s.download(ctx, tag, cacheDir); err != nil {
			return Pack{}, err
		}
		pack.Bytes = s.bytes.Load()
		pack.Requests = int(s.requests.Load())
		pack.Elapsed = time.Since(start)
		if f.OnDownload != nil {
			f.OnDownload(pack)
		}
	}
	if err := writeIndex(cacheDir, serverVersion, tag); err != nil {
		return Pack{}, err
	}
	return pack, nil
}

func packDir(cacheDir, tag string) string { return filepath.Join(cacheDir, tag, "resource_pack") }

// cached reports whether the tag's directory holds everything this version
// reads. A directory from an older fetch lacks the entity files and is
// fetched again, unless it holds the whole pack, which has them.
func cached(cacheDir, tag string) bool {
	b, err := os.ReadFile(filepath.Join(cacheDir, tag, completeMark))
	if err != nil {
		return false
	}
	if string(b) == cacheLayout {
		return true
	}
	_, err = os.Stat(filepath.Join(packDir(cacheDir, tag), "entity"))
	return err == nil
}

// session tallies the traffic of one Fetch.
type session struct {
	Fetcher
	bytes    atomic.Int64
	requests atomic.Int64
}

func (f Fetcher) baseURL() string {
	if f.BaseURL != "" {
		return strings.TrimRight(f.BaseURL, "/")
	}
	return DefaultBaseURL
}

func (f Fetcher) rawBaseURL() string {
	switch {
	case f.RawBaseURL != "":
		return strings.TrimRight(f.RawBaseURL, "/")
	case f.BaseURL != "":
		return f.baseURL() + "/raw"
	}
	return DefaultRawBaseURL
}

type statusError struct {
	url    string
	status string
	code   int
}

func (e *statusError) Error() string { return fmt.Sprintf("atlas: GET %s: %s", e.url, e.status) }

// retryable reports whether a failed attempt is worth repeating: transport
// errors and server-side or throttling statuses, not a missing file.
func (f Fetcher) retryable(err error) bool {
	var se *statusError
	if errors.As(err, &se) {
		return se.code >= 500 || se.code == http.StatusTooManyRequests
	}
	return true
}

// getBody fetches target and returns its body, retrying once after a backoff.
func (s *session) getBody(ctx context.Context, target, accept string) ([]byte, error) {
	var lastErr error
	for attempt := 0; attempt < 2; attempt++ {
		if attempt > 0 {
			backoff := s.Backoff
			if backoff == 0 {
				backoff = defaultBackoff
			}
			select {
			case <-time.After(backoff):
			case <-ctx.Done():
				return nil, ctx.Err()
			}
		}
		body, err := s.getOnce(ctx, target, accept)
		if err == nil {
			return body, nil
		}
		lastErr = err
		if ctx.Err() != nil || !s.retryable(err) {
			break
		}
	}
	return nil, lastErr
}

func (s *session) getOnce(ctx context.Context, target, accept string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, target, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", accept)
	// Asking for gzip ourselves disables the transport's transparent
	// decompression, so the byte count reflects the wire size.
	req.Header.Set("Accept-Encoding", "gzip")
	client := s.Client
	if client == nil {
		client = &http.Client{Timeout: defaultRequestTimeout}
	}
	s.requests.Add(1)
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return nil, &statusError{url: target, status: resp.Status, code: resp.StatusCode}
	}
	counted := &countingReader{r: resp.Body}
	var r io.Reader = counted
	if resp.Header.Get("Content-Encoding") == "gzip" {
		gz, err := gzip.NewReader(counted)
		if err != nil {
			return nil, err
		}
		r = gz
	}
	body, err := io.ReadAll(r)
	// Drain so the count covers the whole response, not just what gzip read.
	_, _ = io.Copy(io.Discard, counted)
	s.bytes.Add(counted.n)
	return body, err
}

func (s *session) listTags(ctx context.Context) ([]string, error) {
	var all []string
	for page := 1; ; page++ {
		body, err := s.getBody(ctx, fmt.Sprintf("%s%s/tags?per_page=100&page=%d", s.baseURL(), repoPath, page), apiAccept)
		if err != nil {
			return nil, err
		}
		var batch []struct {
			Name string `json:"name"`
		}
		if err := json.Unmarshal(body, &batch); err != nil {
			return nil, fmt.Errorf("atlas: decode tag list: %w", err)
		}
		if len(batch) == 0 {
			return all, nil
		}
		for _, t := range batch {
			all = append(all, t.Name)
		}
	}
}

// wanted reports whether p, a repository path, is a file Build reads. The
// cleaned-path check rejects traversal and doubled separators so a tree entry
// can never name a location outside the pack.
func wanted(p string) bool {
	if path.Clean(p) != p || !strings.HasPrefix(p, packPrefix) {
		return false
	}
	rel := strings.TrimPrefix(p, packPrefix)
	switch rel {
	case "blocks.json", "biomes_client.json", "textures/terrain_texture.json", "textures/item_texture.json":
		return true
	}
	for _, dir := range []string{"textures/blocks/", "textures/colormap/", "textures/items/"} {
		if strings.HasPrefix(rel, dir) && len(rel) > len(dir) {
			return true
		}
	}
	return wantedEntityData(rel)
}

// wantedEntityData reports whether rel, a path inside the pack, is an entity
// definition or model. The entity textures are not listed here: only those a
// definition names are fetched, in a second step.
func wantedEntityData(rel string) bool {
	if rel == "models/mobs.json" {
		return true
	}
	if dir, name := path.Split(rel); dir == "entity/" {
		return strings.HasSuffix(name, ".json")
	}
	return strings.HasPrefix(rel, "models/entity/") && strings.HasSuffix(rel, ".geo.json")
}

const entityTextureDir = "textures/entity/"

type treeResponse struct {
	Tree []struct {
		Path string `json:"path"`
		Mode string `json:"mode"`
		Type string `json:"type"`
	} `json:"tree"`
	Truncated bool `json:"truncated"`
}

// listing is what one tree call yields: the files to fetch outright, and the
// entity textures that exist, from which a second step picks those the
// definitions name.
type listing struct {
	files    []string
	entityTx map[string]bool
}

// listFiles asks for the whole tag tree in one API call and keeps the regular
// files Build reads. Symlinks and submodules are skipped by mode.
func (s *session) listFiles(ctx context.Context, tag string) (listing, error) {
	body, err := s.getBody(ctx, fmt.Sprintf("%s%s/git/trees/%s?recursive=1", s.baseURL(), repoPath, url.PathEscape(tag)), apiAccept)
	if err != nil {
		return listing{}, err
	}
	var tree treeResponse
	if err := json.Unmarshal(body, &tree); err != nil {
		return listing{}, fmt.Errorf("atlas: decode tree for %s: %w", tag, err)
	}
	if tree.Truncated {
		return listing{}, fmt.Errorf("atlas: tree for %s is truncated by the API", tag)
	}
	out := listing{entityTx: map[string]bool{}}
	for _, e := range tree.Tree {
		if e.Type != "blob" || (e.Mode != "100644" && e.Mode != "100755") {
			continue
		}
		switch {
		case wanted(e.Path):
			out.files = append(out.files, e.Path)
		case path.Clean(e.Path) == e.Path && strings.HasPrefix(e.Path, packPrefix+entityTextureDir):
			out.entityTx[strings.TrimPrefix(e.Path, packPrefix)] = true
		}
	}
	if len(out.files) == 0 {
		return listing{}, fmt.Errorf("atlas: tree for %s has no resource_pack files to read", tag)
	}
	return out, nil
}

// entityTextureFiles resolves the textures the downloaded definitions name to
// files the tree has, preferring PNG over TGA as Build does.
func entityTextureFiles(packDir string, have map[string]bool) []string {
	var out []string
	for _, ref := range entityTextureRefs(packDir) {
		for _, ext := range []string{".png", ".tga"} {
			if have[ref+ext] {
				out = append(out, packPrefix+ref+ext)
				break
			}
		}
	}
	return out
}

func (s *session) rawURL(tag, p string) string {
	segs := strings.Split(p, "/")
	for i, seg := range segs {
		segs[i] = url.PathEscape(seg)
	}
	return fmt.Sprintf("%s%s/%s/%s", s.rawBaseURL(), rawRepoPath, url.PathEscape(tag), strings.Join(segs, "/"))
}

// download fetches the wanted files into a sibling temp dir and renames it
// into place so an interrupted download never looks like a complete cache
// entry. It returns the number of files written.
func (s *session) download(ctx context.Context, tag, cacheDir string) (int, error) {
	if err := os.MkdirAll(cacheDir, 0o755); err != nil {
		return 0, err
	}
	tmp, err := os.MkdirTemp(cacheDir, ".fetch-")
	if err != nil {
		return 0, err
	}
	defer func() { _ = os.RemoveAll(tmp) }()

	list, err := s.listFiles(ctx, tag)
	if err != nil {
		return 0, err
	}
	if err := s.fetchFiles(ctx, tag, tmp, list.files); err != nil {
		return 0, fmt.Errorf("atlas: fetch %s: %w", tag, err)
	}
	// The definitions are only known once downloaded, so the textures they
	// name are a second round.
	textures := entityTextureFiles(filepath.Join(tmp, "resource_pack"), list.entityTx)
	if err := s.fetchFiles(ctx, tag, tmp, textures); err != nil {
		return 0, fmt.Errorf("atlas: fetch %s: %w", tag, err)
	}
	files := append(list.files, textures...)
	if err := os.WriteFile(filepath.Join(tmp, completeMark), []byte(cacheLayout), 0o644); err != nil {
		return 0, err
	}
	dest := filepath.Join(cacheDir, tag)
	if err := os.RemoveAll(dest); err != nil {
		return 0, err
	}
	if err := os.Rename(tmp, dest); err != nil {
		return 0, err
	}
	return len(files), nil
}

// fetchFiles downloads files concurrently; the first failure cancels the rest.
func (s *session) fetchFiles(ctx context.Context, tag, dest string, files []string) error {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	var (
		wg      sync.WaitGroup
		once    sync.Once
		firstEr error
		jobs    = make(chan string)
	)
	fail := func(err error) {
		once.Do(func() { firstEr = err; cancel() })
	}
	for range min(fileWorkers, len(files)) {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for p := range jobs {
				if ctx.Err() != nil {
					continue
				}
				if err := s.fetchFile(ctx, tag, dest, p); err != nil {
					fail(err)
				}
			}
		}()
	}
	for _, p := range files {
		jobs <- p
	}
	close(jobs)
	wg.Wait()
	return firstEr
}

func (s *session) fetchFile(ctx context.Context, tag, dest, p string) error {
	body, err := s.getBody(ctx, s.rawURL(tag, p), rawAccept)
	if err != nil {
		return err
	}
	target := filepath.Join(dest, filepath.FromSlash(p))
	if !strings.HasPrefix(target, dest+string(filepath.Separator)) {
		return fmt.Errorf("atlas: path %q escapes the cache", p)
	}
	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		return err
	}
	return os.WriteFile(target, body, 0o644)
}

type countingReader struct {
	r io.Reader
	n int64
}

func (c *countingReader) Read(p []byte) (int, error) {
	n, err := c.r.Read(p)
	c.n += int64(n)
	return n, err
}

func readIndex(cacheDir string) map[string]string {
	idx := map[string]string{}
	if b, err := os.ReadFile(filepath.Join(cacheDir, indexFile)); err == nil {
		_ = json.Unmarshal(b, &idx)
	}
	return idx
}

func writeIndex(cacheDir, serverVersion, tag string) error {
	idx := readIndex(cacheDir)
	idx[serverVersion] = tag
	b, err := json.MarshalIndent(idx, "", "  ")
	if err != nil {
		return err
	}
	tmp, err := os.CreateTemp(cacheDir, ".index-")
	if err != nil {
		return err
	}
	_, werr := tmp.Write(b)
	if cerr := tmp.Close(); werr == nil {
		werr = cerr
	}
	if werr != nil {
		_ = os.Remove(tmp.Name())
		return werr
	}
	return os.Rename(tmp.Name(), filepath.Join(cacheDir, indexFile))
}
