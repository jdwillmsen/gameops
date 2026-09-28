package main

import "strings"

type Mode int

const (
	// ModeCI decides what to build and test.
	ModeCI Mode = iota
	// ModeRelease decides what a commit ships.
	ModeRelease
)

// Root files whose change can alter every component in that language: a
// bumped dependency or workspace entry reaches all of them.
var toolchainFiles = map[string][]string{
	"go":         {"go.mod", "go.sum"},
	"typescript": {"package.json", "pnpm-lock.yaml", "pnpm-workspace.yaml"},
}

// These change how every component is checked, so CI reruns them all, but
// they change no shipped code, so they never cause a release.
var ciOnlyEverything = []string{"tools/", ".github/"}

func Affected(ms []Manifest, paths []string, mode Mode) []Manifest {
	out := []Manifest{}
	for _, m := range ms {
		if affects(m, paths, mode) {
			out = append(out, m)
		}
	}
	return out
}

func affects(m Manifest, paths []string, mode Mode) bool {
	prefixes := append([]string{m.Dir}, m.Depends...)
	if mode == ModeCI {
		prefixes = append(prefixes, ciOnlyEverything...)
	}
	for _, p := range paths {
		for _, pre := range prefixes {
			if strings.HasPrefix(p, pre) {
				return true
			}
		}
		for _, f := range toolchainFiles[m.Language] {
			if p == f {
				return true
			}
		}
	}
	return false
}
