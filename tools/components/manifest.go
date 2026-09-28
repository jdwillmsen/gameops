// Command components reads the component.yaml manifests that describe every
// buildable thing in this repo. CI and releases both ask it which components a
// change affects, so the two can never disagree about it.
package main

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"go.yaml.in/yaml/v3"
)

const manifestFile = "component.yaml"

type Manifest struct {
	Name     string   `yaml:"name"`
	Kind     string   `yaml:"kind"`
	Language string   `yaml:"language"`
	Depends  []string `yaml:"depends"`
	Tasks    Tasks    `yaml:"tasks"`
	Release  *Release `yaml:"release"`
	// Dir is repo-relative with a trailing slash, so a prefix match on
	// minecraft/agent/ can never also match a sibling minecraft/agent2/.
	Dir string `yaml:"-"`
}

type Tasks struct {
	Build string `yaml:"build"`
	Test  string `yaml:"test"`
	Lint  string `yaml:"lint"`
}

type Release struct {
	Tag       string   `yaml:"tag"`
	Artifacts []string `yaml:"artifacts"`
	Image     *Image   `yaml:"image"`
}

type Image struct {
	Name             string `yaml:"name"`
	Description      string `yaml:"description"`
	ShortDescription string `yaml:"short_description"`
}

var (
	namePattern = regexp.MustCompile(`^[a-z][a-z0-9-]*$`)
	kinds       = set("service", "frontend", "library", "pack", "job", "cli")
	languages   = set("go", "node", "typescript")
	artifacts   = set("image", "github-asset")
)

func set(xs ...string) map[string]bool {
	m := make(map[string]bool, len(xs))
	for _, x := range xs {
		m[x] = true
	}
	return m
}

func keys(m map[string]bool) string {
	ks := make([]string, 0, len(m))
	for k := range m {
		ks = append(ks, k)
	}
	sort.Strings(ks)
	return strings.Join(ks, ", ")
}

func Load(root string) ([]Manifest, error) {
	var ms []Manifest
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() && (d.Name() == ".git" || d.Name() == "node_modules") {
			return filepath.SkipDir
		}
		if d.IsDir() || d.Name() != manifestFile {
			return nil
		}
		m, err := parse(path)
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(root, filepath.Dir(path))
		if err != nil {
			return err
		}
		m.Dir = filepath.ToSlash(rel) + "/"
		ms = append(ms, m)
		return nil
	})
	if err != nil {
		return nil, err
	}
	sort.Slice(ms, func(i, j int) bool { return ms[i].Name < ms[j].Name })
	return ms, nil
}

func parse(path string) (Manifest, error) {
	f, err := os.Open(path)
	if err != nil {
		return Manifest{}, err
	}
	defer f.Close()
	dec := yaml.NewDecoder(f)
	// A misspelt key would otherwise decode to nothing and silently drop a
	// task or a release.
	dec.KnownFields(true)
	var m Manifest
	if err := dec.Decode(&m); err != nil {
		return Manifest{}, fmt.Errorf("%s: %w", path, err)
	}
	return m, nil
}

// Validate returns every problem at once, so one CI run shows everything a
// new component got wrong.
func Validate(root string, ms []Manifest) []error {
	var errs []error
	bad := func(m Manifest, format string, args ...any) {
		errs = append(errs, fmt.Errorf("%s%s: %s", m.Dir, manifestFile, fmt.Sprintf(format, args...)))
	}
	names := map[string]string{}
	tags := map[string]string{}
	for _, m := range ms {
		if !namePattern.MatchString(m.Name) {
			bad(m, "name %q must match %s", m.Name, namePattern)
		}
		if prev, ok := names[m.Name]; ok {
			bad(m, "name %q already used by %s", m.Name, prev)
		} else {
			names[m.Name] = m.Dir
		}
		if !kinds[m.Kind] {
			bad(m, "kind %q is not one of %s", m.Kind, keys(kinds))
		}
		if !languages[m.Language] {
			bad(m, "language %q is not one of %s", m.Language, keys(languages))
		}
		if m.Tasks.Build == "" {
			bad(m, "tasks.build is required")
		}
		if m.Tasks.Test == "" {
			bad(m, "tasks.test is required")
		}
		for _, d := range m.Depends {
			if !strings.HasSuffix(d, "/") {
				bad(m, "depends %q must end with / (directories only)", d)
				continue
			}
			if _, err := os.Stat(filepath.Join(root, d)); err != nil {
				bad(m, "depends %q does not exist", d)
			}
		}
		if m.Release != nil {
			validateRelease(root, m, tags, bad)
		}
	}
	return errs
}

func validateRelease(root string, m Manifest, tags map[string]string, bad func(Manifest, string, ...any)) {
	r := m.Release
	if m.Kind == "library" {
		bad(m, "a library has no release; drop the release block")
	}
	if !namePattern.MatchString(r.Tag) {
		bad(m, "release.tag %q must match %s", r.Tag, namePattern)
	}
	if prev, ok := tags[r.Tag]; ok {
		bad(m, "release.tag %q already used by %s", r.Tag, prev)
	} else {
		tags[r.Tag] = m.Dir
	}
	wantsImage := false
	for _, a := range r.Artifacts {
		if !artifacts[a] {
			bad(m, "release.artifacts %q is not one of %s", a, keys(artifacts))
		}
		wantsImage = wantsImage || a == "image"
	}
	switch {
	case wantsImage && r.Image == nil:
		bad(m, "release.artifacts has image but release.image is missing")
	case !wantsImage && r.Image != nil:
		bad(m, "release.image is set but release.artifacts has no image")
	}
	if r.Image == nil {
		return
	}
	// These values are written to $GITHUB_OUTPUT as key=value lines, where a
	// newline would start a bogus key.
	if strings.ContainsAny(r.Image.Name+r.Image.Description+r.Image.ShortDescription, "\r\n") {
		bad(m, "release.image descriptions must be one line")
	}
	if !namePattern.MatchString(r.Image.Name) {
		bad(m, "release.image.name %q must match %s", r.Image.Name, namePattern)
	}
	if r.Image.Description == "" {
		bad(m, "release.image.description is required")
	}
	if wantsImage {
		if _, err := os.Stat(filepath.Join(root, m.Dir, "Dockerfile")); errors.Is(err, fs.ErrNotExist) {
			bad(m, "release.artifacts has image but %sDockerfile does not exist", m.Dir)
		}
	}
}
