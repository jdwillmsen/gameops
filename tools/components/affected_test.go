package main

import (
	"reflect"
	"testing"
)

func names(ms []Manifest) []string {
	out := []string{}
	for _, m := range ms {
		out = append(out, m.Name)
	}
	return out
}

func TestAffected(t *testing.T) {
	ms := []Manifest{
		{Name: "agent", Language: "go", Dir: "minecraft/agent/", Depends: []string{"internal/"}},
		{Name: "agent2", Language: "go", Dir: "minecraft/agent2/"},
		{Name: "web", Language: "typescript", Dir: "minecraft/web/", Depends: []string{"packages/ui/"}},
	}
	cases := []struct {
		name  string
		paths []string
		mode  Mode
		want  []string
	}{
		{"own dir", []string{"minecraft/agent/main.go"}, ModeCI, []string{"agent"}},
		{"sibling prefix does not leak", []string{"minecraft/agent2/x.go"}, ModeCI, []string{"agent2"}},
		{"depends", []string{"internal/presenceapi/a.go"}, ModeRelease, []string{"agent"}},
		{"go toolchain hits only go", []string{"go.sum"}, ModeRelease, []string{"agent", "agent2"}},
		{"ts toolchain hits only ts", []string{"pnpm-lock.yaml"}, ModeRelease, []string{"web"}},
		{"root docs hit nothing", []string{"README.md"}, ModeCI, []string{}},
		{"tools rerun everything in CI", []string{"tools/components/main.go"}, ModeCI, []string{"agent", "agent2", "web"}},
		{"workflows rerun everything in CI", []string{".github/workflows/ci.yml"}, ModeCI, []string{"agent", "agent2", "web"}},
		{"tools release nothing", []string{"tools/components/main.go", ".github/workflows/ci.yml"}, ModeRelease, []string{}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := names(Affected(ms, c.paths, c.mode))
			if !reflect.DeepEqual(got, c.want) {
				t.Fatalf("got %v, want %v", got, c.want)
			}
		})
	}
}
