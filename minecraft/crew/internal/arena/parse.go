package arena

import (
	"encoding/json"
	"fmt"
	"strings"
)

// TargetPosition is an entity position read from querytarget. Bedrock reports
// block coordinates as floats.
type TargetPosition struct {
	X float64 `json:"x"`
	Y float64 `json:"y"`
	Z float64 `json:"z"`
}

// ParseTargetPosition reads the first entity's position from querytarget's JSON
// array. Console output may carry a log prefix with brackets of its own, so it
// tries each opening bracket instead of trusting the first one.
func ParseTargetPosition(out string) (TargetPosition, error) {
	for i := 0; i < len(out); i++ {
		if out[i] != '[' {
			continue
		}
		var targets []struct {
			Position TargetPosition `json:"position"`
		}
		if err := json.NewDecoder(strings.NewReader(out[i:])).Decode(&targets); err != nil {
			continue
		}
		if len(targets) == 0 {
			return TargetPosition{}, fmt.Errorf("querytarget returned no entities")
		}
		return targets[0].Position, nil
	}
	return TargetPosition{}, fmt.Errorf("no querytarget JSON in %q", strings.TrimSpace(out))
}

// ParseCount reads a testfor reply. Bedrock lists every target by name, so
// duplicates matter ("Found Zombie, Zombie" is two mobs), and a miss is an
// ERROR line rather than a zero. Log line prefixes are ignored.
func ParseCount(out string) (int, error) {
	found := false
	count := 0
	for _, line := range strings.Split(out, "\n") {
		if i := strings.Index(line, "No targets matched"); i >= 0 {
			found, count = true, 0
			continue
		}
		i := strings.Index(line, "Found ")
		if i < 0 {
			continue
		}
		names := strings.TrimSpace(line[i+len("Found "):])
		if names == "" {
			found, count = true, 0
			continue
		}
		n := 0
		for _, name := range strings.Split(names, ",") {
			if strings.TrimSpace(name) != "" {
				n++
			}
		}
		found, count = true, n
	}
	if !found {
		return 0, fmt.Errorf("no testfor result in console output %q", strings.TrimSpace(out))
	}
	return count, nil
}
