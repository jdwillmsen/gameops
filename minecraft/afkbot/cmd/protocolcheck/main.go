// Command protocolcheck reports whether the version gophertunnel speaks still
// matches the production server's.
//
// The Go replacement for scripts/check-protocol.mjs, which asked the same
// question of bedrock-protocol. The check itself is not optional: the server
// runs Mojang's LATEST and upgrades itself on restart, so the version it
// speaks moves without anyone deciding it should. If gophertunnel falls
// behind, every bot fails to connect at once, and the first symptom is an
// empty server.
//
// Dispatched hourly by the jdw-deployments version-check CronJob, which is the
// only thing with a network path to the LAN-only server.
package main

import (
	"flag"
	"fmt"
	"os"

	"github.com/sandertv/gophertunnel/minecraft/protocol"
)

func main() {
	production := flag.String("production-version", "", "version string the production server reports, e.g. 1.26.45")
	flag.Parse()

	if *production == "" {
		fmt.Fprintln(os.Stderr, "protocolcheck: -production-version is required")
		os.Exit(2)
	}

	fmt.Printf("{\"event\":\"protocol_check\",\"library_version\":%q,\"library_protocol\":%d,\"production_version\":%q}\n",
		protocol.CurrentVersion, protocol.CurrentProtocol, *production)

	if protocol.CurrentVersion == *production {
		fmt.Println("protocolcheck: gophertunnel matches production")
		return
	}

	// Exit 1 rather than failing silently: this runs unattended, and a
	// mismatch that only appears in logs is a mismatch nobody sees. Comparing
	// version strings rather than protocol numbers is deliberate here --
	// gophertunnel exposes one protocol constant, not a table, so there is no
	// way to tell "different string, same protocol" apart from a real gap
	// without a network call this check cannot make.
	fmt.Fprintf(os.Stderr,
		"protocolcheck: gophertunnel speaks %s (protocol %d) but production reports %s; bots may fail to connect\n",
		protocol.CurrentVersion, protocol.CurrentProtocol, *production)
	os.Exit(1)
}
