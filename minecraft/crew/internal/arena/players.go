package arena

import (
	"fmt"
	"strings"
)

// DefaultCrewNames are the twenty crew units and the probe client: the only
// players a boss test may share the server with.
var DefaultCrewNames = []string{
	"Agate", "Basalt", "Cobble", "Dusty", "Ember", "Flint", "Gravel", "Hazel",
	"Ingot", "Jasper", "Kettle", "Loam", "Marble", "Nugget", "Onyx", "Pebble",
	"Quartz", "Rusty", "Slate", "Tinder", "Probe",
}

// NotAloneError reports the non-crew players that made a boss test refuse.
type NotAloneError struct{ Names []string }

func (e *NotAloneError) Error() string {
	return "arena: non-crew players are online: " + strings.Join(e.Names, ", ")
}

// listAttempts bounds how often an unfinished `list` reply is re-read.
const listAttempts = 3

// OnlineNames reads the names line that follows the last "players online:"
// header. Only that line is a player list: the log also carries earlier
// commands' ERROR lines, and one of those landing right after an empty header
// must not count as a player. complete is false when no header has a line
// after it yet, which a caller must not read as an empty server.
func OnlineNames(out string) (names []string, complete bool) {
	lines := strings.Split(out, "\n")
	for i := len(lines) - 1; i >= 0; i-- {
		_, rest, ok := strings.Cut(lines[i], "players online:")
		if !ok {
			continue
		}
		// Text after the colon on the header line itself is still read as
		// names, so a server that prints the list inline is not missed.
		line := strings.TrimSpace(rest)
		if line == "" {
			if i+1 >= len(lines) {
				continue
			}
			line = lines[i+1]
		}
		return splitPlayers(line), true
	}
	return nil, false
}

// splitPlayers drops a log prefix and treats an ERROR or WARN line as no
// players at all.
func splitPlayers(line string) []string {
	line = strings.TrimSpace(line)
	var prefix string
	for strings.HasPrefix(line, "[") {
		end := strings.Index(line, "]")
		if end < 0 {
			break
		}
		prefix += line[:end+1]
		line = strings.TrimSpace(line[end+1:])
	}
	if strings.Contains(prefix, "ERROR") || strings.Contains(prefix, "WARN") {
		return nil
	}
	var names []string
	for _, name := range strings.Split(line, ",") {
		if name = strings.TrimSpace(name); name != "" {
			names = append(names, name)
		}
	}
	return names
}

// NonCrew returns the names that are not in crew.
func NonCrew(names, crew []string) []string {
	known := map[string]bool{}
	for _, c := range crew {
		known[c] = true
	}
	var others []string
	for _, n := range names {
		if !known[n] {
			others = append(others, n)
		}
	}
	return others
}

// CheckAlone lists the server's players and refuses when any is not crew. An
// unreadable list is an error, not a pass: a buffed boss must never be
// summoned on a guess.
func CheckAlone(c Console, crew []string) error {
	var all string
	for range listAttempts {
		out, err := c.Exec("list")
		if err != nil {
			return err
		}
		// Keep earlier replies: a names line that arrives late is read
		// together with the header it belongs to.
		if all != "" {
			all += "\n"
		}
		all += out
		if names, ok := OnlineNames(all); ok {
			if others := NonCrew(names, crew); len(others) > 0 {
				return &NotAloneError{Names: others}
			}
			return nil
		}
	}
	return fmt.Errorf("arena: could not read the player list from %q", strings.TrimSpace(all))
}
