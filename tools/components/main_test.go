package main

import (
	"bytes"
	"encoding/json"
	"os/exec"
	"strconv"
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

// fixtureWith writes a component.yaml under rel in the fixture repo.
func fixtureWith(t *testing.T, root, rel, yaml string) {
	t.Helper()
	writeFile(t, root, rel+"/component.yaml", yaml)
}

func TestCLIGetForReleasableWithoutImage(t *testing.T) {
	root, _, _, _, _ := fixture(t)
	fixtureWith(t, root, "minecraft/tool", `name: tool
kind: cli
language: go
tasks: {build: b, test: go test ./minecraft/tool/...}
release:
  tag: tool
  artifacts: [github-asset]
`)
	fixtureWith(t, root, "minecraft/bare", `name: bare
kind: job
language: go
tasks: {build: b, test: t}
release: {tag: bare}
`)
	for _, c := range []struct{ tag, dir string }{{"tool-v2.0.0", "minecraft/tool"}, {"bare-v2.0.0", "minecraft/bare"}} {
		got := runCLI(t, root, "", "get", "-release-tag", c.tag)
		for _, want := range []string{"dir=" + c.dir + "\n", "version=2.0.0\n", "image=\n", "description=\n", "short_description=\n"} {
			if !strings.Contains(got, want) {
				t.Errorf("%s: output missing %q:\n%s", c.tag, want, got)
			}
		}
	}
	got := runCLI(t, root, "", "get", "-release-tag", "agent-v1.2.3")
	if !strings.Contains(got, "image=minecraft-server-agent\n") {
		t.Errorf("image component lost its image:\n%s", got)
	}
}

func TestCLIGetEmitsOnlyOneLinePerKey(t *testing.T) {
	root, _, _, _, _ := fixture(t)
	for _, bad := range []string{"go test ./a\n  && echo injected=1", "go test ./a\rb", "go test\x00x"} {
		yaml := "name: tool\nkind: cli\nlanguage: go\ntasks:\n  build: b\n  test: " +
			strconv.Quote(bad) + "\nrelease: {tag: tool}\n"
		fixtureWith(t, root, "minecraft/tool", yaml)
		var out bytes.Buffer
		err := run(root, []string{"get", "-release-tag", "tool-v1.0.0"}, nil, &out)
		if err == nil || !strings.Contains(err.Error(), "tasks.test must be one line") {
			t.Errorf("test %q: want one-line error, got %v", bad, err)
		}
		if out.Len() != 0 {
			t.Errorf("test %q: emitted %q despite the error", bad, out.String())
		}
	}
}

func TestOneLine(t *testing.T) {
	for v, want := range map[string]bool{"": true, "go test ./...": true, "a\tb": true, "é ✓": true,
		"a\nb": false, "a\rb": false, "a\x00b": false, "a\x1bb": false, "a\u0085b": false} {
		if got := oneLine(v); got != want {
			t.Errorf("oneLine(%q) = %v, want %v", v, got, want)
		}
	}
}

func TestCLIAffectedRunsEverythingWhenBaseLeftHistory(t *testing.T) {
	root, _, _, _, docs := fixture(t)
	// A force push: the before-SHA is well formed but names a commit this
	// clone never had.
	missing := strings.Repeat("ab", 20)
	var rows []map[string]any
	if err := json.Unmarshal([]byte(runCLI(t, root, "", "affected", "-base", missing, "-head", docs)), &rows); err != nil || len(rows) != 2 {
		t.Fatalf("missing base: want every component, got %v %v", rows, err)
	}
	for _, flagLike := range []string{"--output=/tmp/x", "-h"} {
		if err := json.Unmarshal([]byte(runCLI(t, root, "", "affected", "-base="+flagLike, "-head", docs)), &rows); err != nil || len(rows) != 2 {
			t.Fatalf("base %q: want every component, got %v %v", flagLike, rows, err)
		}
	}
	// An existing base still narrows the plan, so the fallback is not just
	// "always run everything".
	if got := runCLI(t, root, "", "affected", "-base", docs+"~1", "-head", docs); got != "[]\n" {
		t.Fatalf("known base: got %q, want []", got)
	}
}

func TestCLIAffectedRunsEverythingWhenNoMergeBase(t *testing.T) {
	root, _, _, _, docs := fixture(t)
	git(t, root, "checkout", "-q", "--orphan", "unrelated")
	writeFile(t, root, "other.txt", "x")
	git(t, root, "add", "-A")
	git(t, root, "commit", "-qm", "unrelated")
	unrelated := git(t, root, "rev-parse", "HEAD")
	var rows []map[string]any
	if err := json.Unmarshal([]byte(runCLI(t, root, "", "affected", "-base", docs, "-head", unrelated)), &rows); err != nil || len(rows) != 2 {
		t.Fatalf("no merge base: want every component, got %v %v", rows, err)
	}
}

func TestCLICommitsJudgesMergeAgainstFirstParent(t *testing.T) {
	root, base, _, _, _ := fixture(t)
	git(t, root, "checkout", "-q", "-b", "side", base)
	writeFile(t, root, "minecraft/bridge/side.go", "side")
	git(t, root, "add", "-A")
	git(t, root, "commit", "-qm", "side")
	git(t, root, "checkout", "-q", "main")
	// main's own agent change is the other side of the merge: the merge must
	// not be credited to agent for it.
	writeFile(t, root, "minecraft/agent/main.go", "agent")
	git(t, root, "add", "-A")
	git(t, root, "commit", "-qm", "agent")
	git(t, root, "merge", "--no-ff", "-qm", "merge side", "side")
	merge := git(t, root, "rev-parse", "HEAD")

	if got := runCLI(t, root, merge+"\n", "commits", "-component", "agent"); got != "" {
		t.Errorf("agent: merge credited with its other parent's files: %q", got)
	}
	if got := runCLI(t, root, merge+"\n", "commits", "-component", "bridge"); got != merge+"\n" {
		t.Errorf("bridge: merge brought bridge changes in, got %q", got)
	}
}
