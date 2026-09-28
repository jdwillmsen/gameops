package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeFile(t *testing.T, root, rel, body string) {
	t.Helper()
	p := filepath.Join(root, rel)
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

const agentYAML = `name: agent
kind: service
language: go
depends: [internal/]
tasks:
  build: go build ./minecraft/agent/...
  test: go test ./minecraft/agent/...
release:
  tag: agent
  artifacts: [image]
  image:
    name: minecraft-server-agent
    description: Chat agent
    short_description: Chat agent
`

func TestLoadFindsManifestsAndSetsDir(t *testing.T) {
	root := t.TempDir()
	writeFile(t, root, "internal/.keep", "")
	writeFile(t, root, "minecraft/agent/component.yaml", agentYAML)
	writeFile(t, root, "minecraft/agent/Dockerfile", "FROM scratch\n")
	writeFile(t, root, "node_modules/x/component.yaml", "name: ignored\n")

	ms, err := Load(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(ms) != 1 || ms[0].Name != "agent" || ms[0].Dir != "minecraft/agent/" {
		t.Fatalf("got %+v", ms)
	}
	if ms[0].Release.Image.ShortDescription != "Chat agent" {
		t.Fatalf("short_description not decoded: %+v", ms[0].Release.Image)
	}
	if errs := Validate(root, ms); len(errs) != 0 {
		t.Fatalf("valid manifest rejected: %v", errs)
	}
}

func TestLoadRejectsUnknownFields(t *testing.T) {
	root := t.TempDir()
	writeFile(t, root, "a/component.yaml", "name: a\nkind: cli\nlanguage: go\ntaks: {}\n")
	if _, err := Load(root); err == nil || !strings.Contains(err.Error(), "taks") {
		t.Fatalf("want unknown-field error naming taks, got %v", err)
	}
}

func TestValidateReportsEveryProblem(t *testing.T) {
	root := t.TempDir()
	ms := []Manifest{
		{Name: "Bad_Name", Kind: "server", Language: "cobol", Dir: "a/",
			Depends: []string{"nope", "missing/"}},
		{Name: "lib", Kind: "library", Language: "go", Dir: "b/",
			Tasks:   Tasks{Build: "x", Test: "y"},
			Release: &Release{Tag: "lib", Artifacts: []string{"image", "tarball"}}},
		{Name: "lib", Kind: "service", Language: "go", Dir: "c/",
			Tasks:   Tasks{Build: "x", Test: "y"},
			Release: &Release{Tag: "lib", Image: &Image{Name: "c", Description: "two\nlines"}}},
	}
	errs := Validate(root, ms)
	joined := ""
	for _, e := range errs {
		joined += e.Error() + "\n"
	}
	for _, want := range []string{
		`a/component.yaml: name "Bad_Name"`,
		`a/component.yaml: kind "server"`,
		`a/component.yaml: language "cobol"`,
		`a/component.yaml: tasks.build is required`,
		`a/component.yaml: tasks.test is required`,
		`a/component.yaml: depends "nope" must end with /`,
		`a/component.yaml: depends "missing/" does not exist`,
		`b/component.yaml: a library has no release`,
		`b/component.yaml: release.artifacts "tarball"`,
		`b/component.yaml: release.artifacts has image but release.image is missing`,
		`c/component.yaml: name "lib" already used by b/`,
		`c/component.yaml: release.tag "lib" already used by b/`,
		`c/component.yaml: release.image is set but release.artifacts has no image`,
		`c/component.yaml: release.image descriptions must be one line`,
	} {
		if !strings.Contains(joined, want) {
			t.Errorf("missing error %q in:\n%s", want, joined)
		}
	}
}

func TestValidateRequiresDockerfileForImage(t *testing.T) {
	root := t.TempDir()
	writeFile(t, root, "internal/.keep", "")
	writeFile(t, root, "minecraft/agent/component.yaml", agentYAML)
	ms, err := Load(root)
	if err != nil {
		t.Fatal(err)
	}
	errs := Validate(root, ms)
	if len(errs) != 1 || !strings.Contains(errs[0].Error(), "minecraft/agent/Dockerfile does not exist") {
		t.Fatalf("got %v", errs)
	}
}
