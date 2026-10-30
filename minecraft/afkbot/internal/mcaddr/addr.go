// Package mcaddr decides which transport a server address names.
//
// Bedrock servers from 1.26.60 speak NetherNet only, while older ones speak
// RakNet. The two are told apart by the address alone so a deployment moves
// between them by changing one value, with no flag to keep in step.
package mcaddr

import (
	"fmt"
	"net/url"
	"strings"
)

// IsNetherNet reports whether address names a NetherNet server: the HTTP(S)
// URL of its signaling port. Anything else is a RakNet host:port.
func IsNetherNet(address string) bool {
	a := strings.ToLower(address)
	return strings.HasPrefix(a, "http://") || strings.HasPrefix(a, "https://")
}

// ValidateNetherNet checks the shape the NetherNet client accepts, so a bad
// address fails at startup instead of on every reconnect: a URL with a port
// and no path.
func ValidateNetherNet(address string) error {
	u, err := url.Parse(address)
	if err != nil {
		return fmt.Errorf("not a valid URL: %w", err)
	}
	if u.Hostname() == "" || u.Port() == "" || u.Path != "" || u.RawQuery != "" || u.User != nil {
		return fmt.Errorf("%q must look like http://host:port, with a port and no path", address)
	}
	return nil
}
