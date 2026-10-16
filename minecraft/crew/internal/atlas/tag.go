// Package atlas turns Mojang's published vanilla resource pack into a block
// texture atlas and lookup table for the crew camera. Nothing from the pack is
// ever stored in the repository: it is fetched and derived at runtime.
package atlas

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
)

type version [4]int

var tagPattern = regexp.MustCompile(`^v?(\d+(?:\.\d+){2,3})$`)

func parseVersion(s string) (version, bool) {
	m := tagPattern.FindStringSubmatch(s)
	if m == nil {
		return version{}, false
	}
	var v version
	for i, part := range strings.Split(m[1], ".") {
		n, err := strconv.Atoi(part)
		if err != nil {
			return version{}, false
		}
		v[i] = n
	}
	return v, true
}

func (v version) less(o version) bool {
	for i := range v {
		if v[i] != o[i] {
			return v[i] < o[i]
		}
	}
	return false
}

// SelectTag returns the newest release tag that is not newer than the server
// version. Hotfix builds often have no tag of their own, so the nearest lower
// one is the closest available pack. Preview tags carry a suffix that the
// release pattern rejects, so they are never picked.
func SelectTag(serverVersion string, tags []string) (string, error) {
	want, ok := parseVersion(serverVersion)
	if !ok {
		return "", fmt.Errorf("atlas: unparseable server version %q", serverVersion)
	}
	best, found := "", false
	var bestVer version
	for _, tag := range tags {
		v, ok := parseVersion(tag)
		if !ok || want.less(v) {
			continue
		}
		if !found || bestVer.less(v) {
			best, bestVer, found = tag, v, true
		}
	}
	if !found {
		return "", fmt.Errorf("atlas: no bedrock-samples tag at or below %s", serverVersion)
	}
	return best, nil
}
