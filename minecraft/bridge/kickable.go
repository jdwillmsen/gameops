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
// An entry that could change how the console parses the command, or that
// could make the name look different than it displays, is an error rather
// than a dropped name: a quote or backslash could end a quoted name early, a
// leading @ is a selector rather than a player, a control or format
// character (e.g. a zero-width space or a bidi override) can hide inside the
// name invisibly, and non-ASCII whitespace (e.g. a no-break or ideographic
// space) can pass for a plain space without being one. A plain ASCII space
// stays allowed, since gamertags can contain them.
func ParseKickable(raw string) (Kickable, error) {
	k := Kickable{names: make(map[string]struct{})}
	for entry := range strings.SplitSeq(raw, ",") {
		name := strings.TrimSpace(entry)
		if name == "" {
			continue
		}
		if strings.HasPrefix(name, "@") || strings.ContainsAny(name, `"\`) || strings.ContainsFunc(name, isUnsafeRune) {
			return Kickable{}, fmt.Errorf("gamertag %q cannot be passed to kick safely", name)
		}
		k.names[foldASCII(name)] = struct{}{}
	}
	return k, nil
}

// isUnsafeRune reports whether r has no place in a gamertag: a control
// character, a format character (invisible but not a control, such as a
// zero-width space or a bidi override), or any non-ASCII whitespace (which
// can pass for a plain space without being one). A plain ASCII space is
// none of these, so it stays allowed.
func isUnsafeRune(r rune) bool {
	if unicode.IsControl(r) || unicode.Is(unicode.Cf, r) {
		return true
	}
	return r > 0x7F && unicode.IsSpace(r)
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
