package atlas

import (
	"archive/tar"
	"compress/gzip"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"strings"
)

// DefaultBaseURL is the GitHub REST API root.
const DefaultBaseURL = "https://api.github.com"

const (
	repoPath     = "/repos/Mojang/bedrock-samples"
	packPrefix   = "resource_pack/"
	completeMark = ".complete"
	indexFile    = "index.json"
)

// Pack is a resource pack extracted on local disk.
type Pack struct {
	Tag string
	// Dir is the extracted resource_pack directory.
	Dir string
	// Bytes is the compressed download size; zero when served from the cache.
	Bytes int64
}

// Fetcher downloads the vanilla resource pack. The zero value uses the public
// GitHub API; tests inject a client and base URL.
type Fetcher struct {
	Client  *http.Client
	BaseURL string
}

// Fetch downloads the resource pack for the release tag nearest below
// serverVersion into cacheDir/<tag>/. Repeat calls for a version already
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
	tags, err := f.listTags(ctx)
	if err != nil {
		return Pack{}, err
	}
	tag, err := SelectTag(serverVersion, tags)
	if err != nil {
		return Pack{}, err
	}
	pack := Pack{Tag: tag, Dir: packDir(cacheDir, tag)}
	if !cached(cacheDir, tag) {
		if pack.Bytes, err = f.download(ctx, tag, cacheDir); err != nil {
			return Pack{}, err
		}
	}
	if err := writeIndex(cacheDir, serverVersion, tag); err != nil {
		return Pack{}, err
	}
	return pack, nil
}

func packDir(cacheDir, tag string) string { return filepath.Join(cacheDir, tag, "resource_pack") }

func cached(cacheDir, tag string) bool {
	_, err := os.Stat(filepath.Join(cacheDir, tag, completeMark))
	return err == nil
}

func (f Fetcher) baseURL() string {
	if f.BaseURL != "" {
		return strings.TrimRight(f.BaseURL, "/")
	}
	return DefaultBaseURL
}

func (f Fetcher) get(ctx context.Context, url string) (*http.Response, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	client := f.Client
	if client == nil {
		client = http.DefaultClient
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusOK {
		_ = resp.Body.Close()
		return nil, fmt.Errorf("atlas: GET %s: %s", url, resp.Status)
	}
	return resp, nil
}

func (f Fetcher) listTags(ctx context.Context) ([]string, error) {
	var all []string
	for page := 1; ; page++ {
		resp, err := f.get(ctx, fmt.Sprintf("%s%s/tags?per_page=100&page=%d", f.baseURL(), repoPath, page))
		if err != nil {
			return nil, err
		}
		var batch []struct {
			Name string `json:"name"`
		}
		err = json.NewDecoder(resp.Body).Decode(&batch)
		_ = resp.Body.Close()
		if err != nil {
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

// download extracts into a sibling temp dir and renames it into place so an
// interrupted download never looks like a complete cache entry.
func (f Fetcher) download(ctx context.Context, tag, cacheDir string) (int64, error) {
	if err := os.MkdirAll(cacheDir, 0o755); err != nil {
		return 0, err
	}
	tmp, err := os.MkdirTemp(cacheDir, ".fetch-")
	if err != nil {
		return 0, err
	}
	defer func() { _ = os.RemoveAll(tmp) }()

	resp, err := f.get(ctx, fmt.Sprintf("%s%s/tarball/refs/tags/%s", f.baseURL(), repoPath, tag))
	if err != nil {
		return 0, err
	}
	defer func() { _ = resp.Body.Close() }()
	body := &countingReader{r: resp.Body}
	if err := extractResourcePack(body, tmp); err != nil {
		return 0, fmt.Errorf("atlas: extract %s: %w", tag, err)
	}
	// Drain so the byte count covers the whole download, not just what gzip read.
	_, _ = io.Copy(io.Discard, body)
	if err := os.WriteFile(filepath.Join(tmp, completeMark), nil, 0o644); err != nil {
		return 0, err
	}
	dest := filepath.Join(cacheDir, tag)
	if err := os.RemoveAll(dest); err != nil {
		return 0, err
	}
	if err := os.Rename(tmp, dest); err != nil {
		return 0, err
	}
	return body.n, nil
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

func extractResourcePack(r io.Reader, dest string) error {
	gz, err := gzip.NewReader(r)
	if err != nil {
		return err
	}
	tr := tar.NewReader(gz)
	for {
		hdr, err := tr.Next()
		if errors.Is(err, io.EOF) {
			return nil
		}
		if err != nil {
			return err
		}
		if hdr.Typeflag != tar.TypeReg {
			continue
		}
		// The tarball nests everything under a generated "<owner>-<repo>-<sha>/" root.
		_, rel, ok := strings.Cut(hdr.Name, "/")
		if !ok {
			continue
		}
		rel = path.Clean(rel)
		// Clean resolves any ".." before the prefix test, so entries cannot escape dest.
		if !strings.HasPrefix(rel, packPrefix) {
			continue
		}
		target := filepath.Join(dest, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
			return err
		}
		out, err := os.Create(target)
		if err != nil {
			return err
		}
		_, copyErr := io.Copy(out, tr)
		if closeErr := out.Close(); copyErr == nil {
			copyErr = closeErr
		}
		if copyErr != nil {
			return copyErr
		}
	}
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
