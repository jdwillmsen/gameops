package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strings"
)

// ErrCommandNotAllowed is returned when a command does not match any
// allowlisted template. It is the only path by which a command is refused
// or accepted — there is no free-text fallback.
var ErrCommandNotAllowed = errors.New("command not allowed")

// target matches a single selector or player name token: a Bedrock selector
// (@a, @p, @s, @r, @e, optionally with [args]) or a bare name with no
// whitespace (so a caller cannot smuggle a second command via a space).
const targetPattern = `(@[aprse](?:\[[^\]\n]*\])?|[^\s\n"]+)`

var rules = []struct {
	name string
	re   *regexp.Regexp
	// validate runs after a regex match for templates that need more than
	// syntax checking (e.g. tellraw's JSON payload).
	validate func(matches []string) error
}{
	{
		name: "list",
		re:   regexp.MustCompile(`^list$`),
	},
	{
		name: "say",
		// Bedrock's `say` takes the remainder of the line as the message;
		// disallow embedded newlines/carriage returns so one command can't
		// smuggle a second console line.
		re: regexp.MustCompile(`^say (.+)$`),
	},
	{
		name: "title",
		re:   regexp.MustCompile(`^title ` + targetPattern + ` (title|subtitle|actionbar|clear|reset)(?: (.+))?$`),
	},
	{
		name: "tellraw",
		re:   regexp.MustCompile(`^tellraw ` + targetPattern + ` (\{.+\})$`),
		validate: func(m []string) error {
			var v any
			if err := json.Unmarshal([]byte(m[2]), &v); err != nil {
				return fmt.Errorf("tellraw payload is not valid JSON: %w", err)
			}
			obj, ok := v.(map[string]any)
			if !ok {
				return fmt.Errorf("tellraw payload must be a JSON object")
			}
			if _, ok := obj["rawtext"]; !ok {
				return fmt.Errorf(`tellraw payload must contain "rawtext"`)
			}
			return nil
		},
	},
	{
		name: "time_query",
		re:   regexp.MustCompile(`^time query (day|daytime|gametime)$`),
	},
	{
		name: "gamerule_query",
		// A bare gamerule name with no value is a read-only query in
		// Bedrock; a second token would be a write and must not match.
		re: regexp.MustCompile(`^gamerule ([A-Za-z][A-Za-z0-9]*)$`),
	},
}

// CheckAllowlist reports whether cmd matches an allowlisted template. It
// returns the matched rule name on success, or ErrCommandNotAllowed (wrapped
// with a reason) on refusal.
func CheckAllowlist(cmd string) (string, error) {
	if cmd == "" {
		return "", fmt.Errorf("%w: empty command", ErrCommandNotAllowed)
	}
	if strings.ContainsAny(cmd, "\n\r") {
		return "", fmt.Errorf("%w: command contains a newline", ErrCommandNotAllowed)
	}

	for _, r := range rules {
		m := r.re.FindStringSubmatch(cmd)
		if m == nil {
			continue
		}
		if r.validate != nil {
			if err := r.validate(m); err != nil {
				return "", fmt.Errorf("%w: %s: %s", ErrCommandNotAllowed, r.name, err)
			}
		}
		return r.name, nil
	}
	return "", fmt.Errorf("%w: %q matches no allowlisted template", ErrCommandNotAllowed, cmd)
}
