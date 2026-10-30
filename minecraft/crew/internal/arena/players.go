package arena

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
)

// DefaultCrewNames are the twenty-two crew units and the probe client: the only
// players a boss test may share the server with.
var DefaultCrewNames = []string{
	"Agate", "Basalt", "Cobble", "Dusty", "Ember", "Flint", "Gravel", "Hazel",
	"Ingot", "Jasper", "Kettle", "Loam", "Marble", "Nugget", "Onyx", "Pebble",
	"Quartz", "Rusty", "Slate", "Tinder", "Umber", "Vein", "Probe",
}

// NotAloneError reports the non-crew players that made a boss test refuse.
type NotAloneError struct{ Names []string }

func (e *NotAloneError) Error() string {
	return "arena: non-crew players are online: " + strings.Join(e.Names, ", ")
}

// listAttempts bounds how often an unfinished `list` reply is re-read.
const listAttempts = 3

var listHeader = regexp.MustCompile(`There are (\d+)/\d+ players online:(.*)$`)

// OnlineNames reads the player list after the last "players online:" header.
// The header's count decides how many names to read: ERROR and WARN lines from
// earlier commands are skipped, and a names line the log wrapped is joined
// back together. complete is false when the names have not all arrived, or
// when the count is missing or does not match them; a caller must read that
// as "maybe not alone", never as an empty server.
func OnlineNames(out string) (names []string, complete bool) {
	lines := strings.Split(out, "\n")
	for i := len(lines) - 1; i >= 0; i-- {
		if !strings.Contains(lines[i], "players online:") {
			continue
		}
		m := listHeader.FindStringSubmatch(lines[i])
		if m == nil {
			return nil, false
		}
		want, err := strconv.Atoi(m[1])
		if err != nil {
			return nil, false
		}
		names, status := collectNames(m[2], lines[i+1:], want)
		switch status {
		case listComplete:
			return names, true
		case listMismatch:
			return nil, false
		}
		// The names after this header have not arrived yet; an earlier
		// header's late names line may already be complete.
	}
	return nil, false
}

type listStatus int

const (
	listShort listStatus = iota
	listComplete
	listMismatch
)

// collectNames reads want names starting with the text after the header's
// colon, so a server that prints the list inline is not missed.
func collectNames(inline string, rest []string, want int) ([]string, listStatus) {
	if want == 0 {
		return nil, listComplete
	}
	joined := strings.TrimSpace(inline)
	check := func() ([]string, listStatus) {
		names := splitPlayers(joined)
		switch {
		case len(names) > want:
			return nil, listMismatch
		case len(names) == want && !strings.HasSuffix(joined, ","):
			return names, listComplete
		}
		return nil, listShort
	}
	if names, status := check(); status != listShort {
		return names, status
	}
	for _, line := range rest {
		if strings.Contains(line, "players online:") {
			break
		}
		body, noise := logBody(line)
		if noise || body == "" {
			continue
		}
		// A wrapped line continues the previous one exactly, which may be
		// mid-name; a list that does not add up is refused either way.
		joined += body
		if names, status := check(); status != listShort {
			return names, status
		}
	}
	return nil, listShort
}

// logBody drops a log prefix and reports an ERROR or WARN line as noise.
func logBody(line string) (body string, noise bool) {
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
	return line, strings.Contains(prefix, "ERROR") || strings.Contains(prefix, "WARN")
}

func splitPlayers(list string) []string {
	var names []string
	for _, name := range strings.Split(list, ",") {
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
