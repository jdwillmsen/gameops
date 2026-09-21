// Command protocolcheck reports whether the version gophertunnel speaks still
// matches the production server's.
//
// The check is not optional: the server runs Mojang's LATEST and upgrades
// itself on restart, so the version it speaks moves without anyone deciding
// it should. If gophertunnel falls behind, every bot fails to connect at
// once, and the first symptom is an empty server.
//
// Dispatched hourly by the jdw-deployments version-check CronJob, which is the
// only thing with a network path to the LAN-only server.
package main

import (
	"flag"
	"fmt"
	"os"
	"runtime/debug"

	"github.com/sandertv/gophertunnel/minecraft/protocol"
)

const gophertunnelModule = "github.com/sandertv/gophertunnel"

// vendoredVersion reports the gophertunnel module version this binary was
// built against, read from the build info rather than parsed out of go.mod so
// it stays correct under a replace directive.
func vendoredVersion() string {
	info, ok := debug.ReadBuildInfo()
	if !ok {
		return ""
	}
	for _, dep := range info.Deps {
		if dep.Path != gophertunnelModule {
			continue
		}
		if dep.Replace != nil {
			return dep.Replace.Version
		}
		return dep.Version
	}
	return ""
}

// emit appends key=value pairs to the GitHub step output file when running
// under Actions, so callers can branch on the result without re-deriving it.
//
// Every failure is returned rather than swallowed, for the same reason the
// mismatch path below exits 1: this runs unattended, and the workflow decides
// what to do next by reading this file. A write that fails silently leaves the
// next step reading an empty string for `status` and `changed`, which is not
// "no change" -- it is no answer, and it looks exactly like one.
//
// Close is called explicitly rather than deferred: a buffered write can fail
// at close and nowhere else, which is the case the deferred form drops.
func emit(pairs map[string]string) error {
	path := os.Getenv("GITHUB_OUTPUT")
	if path == "" {
		return nil
	}
	f, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		return fmt.Errorf("open %s: %w", path, err)
	}
	for k, v := range pairs {
		if _, err := fmt.Fprintf(f, "%s=%s\n", k, v); err != nil {
			f.Close()
			return fmt.Errorf("write %s to %s: %w", k, path, err)
		}
	}
	if err := f.Close(); err != nil {
		return fmt.Errorf("close %s: %w", path, err)
	}
	return nil
}

// emitOrDie reports an unwritable step output as a failure of the run.
//
// Exit 2, the same code as a usage error, because that is what this is: the
// check could not tell anyone its answer, which is a different thing from a
// protocol mismatch and must not be mistaken for one.
func emitOrDie(pairs map[string]string) {
	if err := emit(pairs); err != nil {
		fmt.Fprintf(os.Stderr, "protocolcheck: cannot report the result: %v\n", err)
		os.Exit(2)
	}
}

// status describes how the library version relates to production, and whether
// anyone can act on it.
type status string

const (
	statusMatch             status = "match"
	statusBehind            status = "behind"
	statusBlockedOnUpstream status = "blocked-on-upstream"
)

// decide classifies a protocol check. A mismatch splits two ways and only one
// of them is anyone's to act on: when upstream has published a newer module
// than the one vendored here, the gap is ours; when the vendored module IS
// upstream's newest, nothing can be bumped until Mojang's change lands there.
// An unknown vendored or upstream version falls back to statusBehind, so a
// build that cannot read its own module graph still fails loudly.
func decide(libraryVersion, productionVersion, vendored, upstream string) status {
	if libraryVersion == productionVersion {
		return statusMatch
	}
	if upstream != "" && vendored != "" && upstream == vendored {
		return statusBlockedOnUpstream
	}
	return statusBehind
}

func main() {
	production := flag.String("production-version", "", "version string the production server reports, e.g. 1.26.45")
	upstream := flag.String("upstream-version", "", "newest gophertunnel module version available upstream, e.g. v1.61.0; optional")
	flag.Parse()

	if *production == "" {
		fmt.Fprintln(os.Stderr, "protocolcheck: -production-version is required")
		os.Exit(2)
	}

	vendored := vendoredVersion()

	fmt.Printf("{\"event\":\"protocol_check\",\"library_version\":%q,\"library_protocol\":%d,\"production_version\":%q,\"gophertunnel_vendored\":%q,\"gophertunnel_upstream\":%q}\n",
		protocol.CurrentVersion, protocol.CurrentProtocol, *production, vendored, *upstream)

	result := decide(protocol.CurrentVersion, *production, vendored, *upstream)

	if result == statusMatch {
		fmt.Println("protocolcheck: gophertunnel matches production")
		emitOrDie(map[string]string{
			"status":             string(statusMatch),
			"changed":            "false",
			"library_version":    protocol.CurrentVersion,
			"production_version": *production,
		})
		return
	}

	// Comparing version strings rather than protocol numbers is deliberate --
	// gophertunnel exposes one protocol constant, not a table, so there is no
	// way to tell "different string, same protocol" apart from a real gap
	// without a network call this check cannot make.

	emitOrDie(map[string]string{
		"status":             string(result),
		"changed":            "true",
		"library_version":    protocol.CurrentVersion,
		"production_version": *production,
		"vendored_version":   vendored,
		"upstream_version":   *upstream,
	})

	if result == statusBlockedOnUpstream {
		fmt.Printf("::warning title=Protocol gap blocked on upstream::gophertunnel speaks %s (protocol %d) but production reports %s; %s is already the newest gophertunnel release, so there is no bump to take yet\n",
			protocol.CurrentVersion, protocol.CurrentProtocol, *production, vendored)
		fmt.Fprintf(os.Stderr,
			"protocolcheck: gophertunnel speaks %s (protocol %d) but production reports %s; vendored %s is already upstream's newest, nothing to bump\n",
			protocol.CurrentVersion, protocol.CurrentProtocol, *production, vendored)
		return
	}

	// Exit 1 rather than failing silently: this runs unattended, and a
	// mismatch that only appears in logs is a mismatch nobody sees.
	fmt.Fprintf(os.Stderr,
		"protocolcheck: gophertunnel speaks %s (protocol %d) but production reports %s; bots may fail to connect\n",
		protocol.CurrentVersion, protocol.CurrentProtocol, *production)
	os.Exit(1)
}
