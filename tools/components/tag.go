package main

import (
	"fmt"
	"regexp"
)

// Greedy on the prefix so a component tag that itself contains "-v<digit>"
// still splits at the last one.
var releaseTag = regexp.MustCompile(`^([a-z][a-z0-9-]*)-v([0-9]+\.[0-9]+\.[0-9]+(?:-[0-9A-Za-z.-]+)?)$`)

func ParseReleaseTag(tag string) (prefix, version string, err error) {
	m := releaseTag.FindStringSubmatch(tag)
	if m == nil {
		return "", "", fmt.Errorf("release tag %q is not <component>-v<semver>", tag)
	}
	return m[1], m[2], nil
}
