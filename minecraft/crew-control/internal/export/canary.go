package export

import (
	"encoding/json"
	"net/netip"
	"regexp"
	"strings"
)

// Finding is one thing the canary scan objects to.
type Finding struct {
	Rule  string
	Match string
}

var (
	coordTriple = regexp.MustCompile(`-?\d+(?:\.\d+)?(?:[\s,;]+-?\d+(?:\.\d+)?){2}`)
	ipv4        = regexp.MustCompile(`\d{1,3}(?:\.\d{1,3}){3}`)
	ipv6Cand    = regexp.MustCompile(`[0-9A-Fa-f:.]*:[0-9A-Fa-f:.]*:[0-9A-Fa-f:.]*`)
	tokenPrefix = regexp.MustCompile(`(?i)(?:gh[pousr]_|github_pat_|xox[abpr]-|sk-|AKIA|eyJ|bearer\b)`)
	longRun     = regexp.MustCompile(`[A-Za-z0-9+/_=-]{40,}`)
	hexRun      = regexp.MustCompile(`[0-9a-fA-F]{32,}`)
	envName     = regexp.MustCompile(`\b[A-Z][A-Z0-9]*(?:_[A-Z0-9]+)+\b`)
	chatLine    = regexp.MustCompile(`(?i)<[^>\s]{1,32}>|\[(?:chat|server|say)\]|\b(?:says|whispers|joined the game|left the game|was slain|was shot|ignore (?:all |the |any )?(?:previous|above|prior))\b`)
	plainValue  = regexp.MustCompile(`^[A-Za-z0-9_.:+-]*$`)
)

// Scan is the second layer behind the allow-list: it reads the final output
// and reports anything of a shape that must never leave, whatever produced
// it. The export refuses to answer if it finds any, and the tests run it on
// every output they build, so a rule that stops holding fails the build.
func Scan(b []byte) []Finding {
	var doc any
	if json.Unmarshal(b, &doc) != nil {
		return []Finding{{Rule: "not_json"}}
	}
	var out []Finding
	walk(doc, func(s string) { out = append(out, scanString(s)...) })
	return out
}

func walk(v any, f func(string)) {
	switch t := v.(type) {
	case string:
		f(t)
	case []any:
		for _, x := range t {
			walk(x, f)
		}
	case map[string]any:
		for k, x := range t {
			f(k)
			walk(x, f)
		}
	}
}

func scanString(s string) []Finding {
	var out []Finding
	hit := func(rule, m string) { out = append(out, Finding{Rule: rule, Match: m}) }
	if m := coordTriple.FindString(s); m != "" {
		hit("coordinates", m)
	}
	for _, m := range ipv4.FindAllString(s, -1) {
		if _, err := netip.ParseAddr(m); err == nil {
			hit("ipv4", m)
		}
	}
	for _, m := range ipv6Cand.FindAllString(s, -1) {
		if _, err := netip.ParseAddr(strings.Trim(m, ".")); err == nil {
			hit("ipv6", m)
		}
	}
	if m := tokenPrefix.FindString(s); m != "" {
		hit("token", m)
	}
	if m := longRun.FindString(s); m != "" {
		hit("token", m)
	}
	if m := hexRun.FindString(s); m != "" {
		hit("token", m)
	}
	if m := envName.FindString(s); m != "" {
		hit("env_name", m)
	}
	if m := chatLine.FindString(s); m != "" {
		hit("player_text", m)
	}
	if !plainValue.MatchString(s) {
		hit("free_text", s)
	}
	return out
}
