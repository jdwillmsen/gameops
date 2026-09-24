package main

import (
	"fmt"
	"strings"
	"unicode"
)

// Kickable is the set of gamertags `kick` may name: the server's own actors
// (the agent and the AFK bots), never a real player, so a leaked bridge token
// still cannot remove one. The zero value refuses every kick.
type Kickable struct {
	names map[string]struct{}
}

// ParseKickable reads BRIDGE_KICKABLE's comma-separated gamertags. Blank
// entries are skipped, so an empty value or a trailing comma is harmless.
// An entry that could change how the console parses the command is an error
// rather than a dropped name: a quote or backslash could end a quoted name
// early, and a leading @ is a selector, not a player.
func ParseKickable(raw string) (Kickable, error) {
	k := Kickable{names: make(map[string]struct{})}
	for entry := range strings.SplitSeq(raw, ",") {
		name := strings.TrimSpace(entry)
		if name == "" {
			continue
		}
		if strings.HasPrefix(name, "@") || strings.ContainsAny(name, `"\`) || strings.ContainsFunc(name, unicode.IsControl) {
			return Kickable{}, fmt.Errorf("gamertag %q cannot be passed to kick safely", name)
		}
		k.names[foldASCII(name)] = struct{}{}
	}
	return k, nil
}

// Len reports how many distinct gamertags are kickable.
func (k Kickable) Len() int {
	return len(k.names)
}

// Contains reports whether name is kickable, ignoring ASCII case only. Full
// Unicode folding would accept names the operator never listed (the Kelvin
// sign folds to "k"), and whom such a name reaches is the console's call,
// not this check's.
func (k Kickable) Contains(name string) bool {
	_, ok := k.names[foldASCII(name)]
	return ok
}

func foldASCII(s string) string {
	b := []byte(s)
	for i, c := range b {
		if 'A' <= c && c <= 'Z' {
			b[i] = c + ('a' - 'A')
		}
	}
	return string(b)
}
