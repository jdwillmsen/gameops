package main

import (
	"bytes"
	"encoding/json"
	"os/exec"
	"strings"
	"testing"
)

func git(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	cmd.Env = append(cmd.Environ(),
		"GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@example.invalid",
		"GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@example.invalid",
		"GIT_CONFIG_GLOBAL=/dev/null")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
	return strings.TrimSpace(string(out))
}

// fixture builds a repo with two components and returns its root and the
// SHAs of: the base commit, a bridge-only commit, a shared-code commit and a
// docs-only commit.
func fixture(t *testing.T) (root string, base, bridge, shared, docs string) {
	root = t.TempDir()
	git(t, root, "init", "-q", "-b", "main")
	writeFile(t, root, "internal/.keep", "")
	writeFile(t, root, "minecraft/agent/component.yaml", agentYAML)
	writeFile(t, root, "minecraft/agent/Dockerfile", "FROM scratch\n")
	writeFile(t, root, "minecraft/bridge/component.yaml",
		"name: bridge\nkind: service\nlanguage: go\ntasks: {build: b, test: t}\n")
	git(t, root, "add", "-A")
	git(t, root, "commit", "-qm", "base")
	base = git(t, root, "rev-parse", "HEAD")
	commit := func(rel, msg string) string {
		writeFile(t, root, rel, msg)
		git(t, root, "add", "-A")
		git(t, root, "commit", "-qm", msg)
		return git(t, root, "rev-parse", "HEAD")
	}
	bridge = commit("minecraft/bridge/main.go", "bridge")
	shared = commit("internal/x.go", "shared")
	docs = commit("README.md", "docs")
	return
}

func runCLI(t *testing.T, root, stdin string, args ...string) string {
	t.Helper()
	var out bytes.Buffer
	if err := run(root, args, strings.NewReader(stdin), &out); err != nil {
		t.Fatalf("%v: %v", args, err)
	}
	return out.String()
}

func TestCLICommitsKeepsOnlyComponentCommits(t *testing.T) {
	root, base, bridge, shared, docs := fixture(t)
	got := runCLI(t, root, strings.Join([]string{base, bridge, shared, docs}, "\n"),
		"commits", "-component", "agent")
	want := base + "\n" + shared + "\n"
	if got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
}

func TestCLIAffectedMatrix(t *testing.T) {
	root, base, _, _, docs := fixture(t)
	var rows []map[string]any
	if err := json.Unmarshal([]byte(runCLI(t, root, "", "affected", "-base", base, "-head", docs)), &rows); err != nil {
		t.Fatal(err)
	}
	if len(rows) != 2 || rows[0]["name"] != "agent" || rows[0]["image"] != true || rows[1]["image"] != false {
		t.Fatalf("got %v", rows)
	}
	for _, zero := range []string{"", "0000000000000000000000000000000000000000"} {
		if err := json.Unmarshal([]byte(runCLI(t, root, "", "affected", "-base", zero, "-head", docs)), &rows); err != nil || len(rows) != 2 {
			t.Fatalf("base %q: want every component, got %v %v", zero, rows, err)
		}
	}
	if got := runCLI(t, root, "", "affected", "-base", docs+"~1", "-head", docs); got != "[]\n" {
		t.Fatalf("docs-only change: got %q, want []", got)
	}
}

func TestCLIGetAndReleasable(t *testing.T) {
	root, _, _, _, _ := fixture(t)
	if got := runCLI(t, root, "", "releasable"); got != "agent agent\n" {
		t.Fatalf("releasable: got %q", got)
	}
	got := runCLI(t, root, "", "get", "-release-tag", "agent-v1.2.3")
	for _, want := range []string{"name=agent\n", "dir=minecraft/agent\n", "version=1.2.3\n",
		"image=minecraft-server-agent\n", "description=Chat agent\n", "test=go test ./minecraft/agent/...\n"} {
		if !strings.Contains(got, want) {
			t.Errorf("get output missing %q:\n%s", want, got)
		}
	}
	var out bytes.Buffer
	if err := run(root, []string{"get", "-release-tag", "nobody-v1.0.0"}, nil, &out); err == nil {
		t.Fatal("unknown tag prefix: want error")
	}
}
