package auth

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"unicode/utf8"
)

const maxOperators = 8

// The id names a file in the invites directory, so its shape is also what
// keeps a path out of that lookup.
var operatorID = regexp.MustCompile(`^[a-z][a-z0-9-]{1,15}$`)

type Operator struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

// ParseOperators reads CREW_OPERATORS. Empty means nobody can sign in.
func ParseOperators(raw string) ([]Operator, error) {
	if strings.TrimSpace(raw) == "" {
		return nil, nil
	}
	dec := json.NewDecoder(strings.NewReader(raw))
	dec.DisallowUnknownFields()
	var ops []Operator
	if err := dec.Decode(&ops); err != nil {
		return nil, fmt.Errorf("operators: %w", err)
	}
	if dec.More() {
		return nil, errors.New("operators: trailing data")
	}
	if len(ops) > maxOperators {
		return nil, fmt.Errorf("operators: at most %d", maxOperators)
	}
	seen := map[string]bool{}
	for _, o := range ops {
		switch {
		case !operatorID.MatchString(o.ID):
			return nil, fmt.Errorf("operators: id %q is not a short lower-case slug", o.ID)
		case seen[o.ID]:
			return nil, fmt.Errorf("operators: id %q is listed twice", o.ID)
		case strings.TrimSpace(o.Name) == "" || utf8.RuneCountInString(o.Name) > 64 || bytes.ContainsAny([]byte(o.Name), "\x00\r\n"):
			return nil, fmt.Errorf("operators: %q needs a name of up to 64 characters", o.ID)
		}
		seen[o.ID] = true
	}
	return ops, nil
}
