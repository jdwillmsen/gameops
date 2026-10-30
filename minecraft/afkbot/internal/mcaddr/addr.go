// Package mcaddr decides which transport a server address names.
//
// Bedrock servers from 1.26.60 speak NetherNet only, while older ones speak
// RakNet. The two are told apart by the address alone so a deployment moves
// between them by changing one value, with no flag to keep in step.
package mcaddr

import (
	"fmt"
	"net"
	"net/url"
	"strconv"
	"strings"
)

// IsNetherNet reports whether address names a NetherNet server: the HTTP(S)
// URL of its signaling port. Anything else is a RakNet host:port.
func IsNetherNet(address string) bool {
	a := strings.ToLower(address)
	return strings.HasPrefix(a, "http://") || strings.HasPrefix(a, "https://")
}

// Resolve returns the address to dial for an MC_HOST/MC_PORT pair, the same
// rule the agent applies: a NetherNet URL keeps its own port and otherwise
// takes port; anything else is a RakNet host:port. A trailing '/' is dropped.
// It rejects what the NetherNet client would refuse on every reconnect, so a
// bad address fails at startup.
func Resolve(host string, port int) (string, error) {
	if !IsNetherNet(host) {
		return net.JoinHostPort(host, strconv.Itoa(port)), nil
	}
	u, err := url.Parse(host)
	if err != nil || u.Hostname() == "" || (u.Path != "" && u.Path != "/") || u.RawQuery != "" || u.User != nil {
		return "", fmt.Errorf("must be a bare http(s)://host[:port] URL when it names a NetherNet server, got %q", host)
	}
	if u.Port() == "" {
		u.Host = net.JoinHostPort(u.Hostname(), strconv.Itoa(port))
	}
	u.Path = ""
	return u.String(), nil
}

// URLPort returns the port a NetherNet URL carries, or "" when it has none or
// host is not a URL.
func URLPort(host string) string {
	if !IsNetherNet(host) {
		return ""
	}
	u, err := url.Parse(host)
	if err != nil {
		return ""
	}
	return u.Port()
}
