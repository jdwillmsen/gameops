package arena

import (
	"fmt"
	"strings"
)

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
