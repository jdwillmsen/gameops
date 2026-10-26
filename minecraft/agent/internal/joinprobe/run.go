package joinprobe

import (
	"context"
	"time"

	"github.com/jdwillmsen/gameops/minecraft/agent/pkg/logging"
)

// Run probes address every interval until ctx ends, publishing each result.
//
// The first probe runs immediately rather than after one interval: a process
// that has just started and publishes nothing is indistinguishable, to the
// rules reading these gauges, from one that cannot reach the server at all.
func Run(ctx context.Context, address string, forceProtocol int32, interval time.Duration, log *logging.Logger) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	probeLoop(ctx, address, forceProtocol, ticker.C, log)
}

// probeLoop is Run with the tick source passed in rather than built here.
// The tests need one they drive by hand, because against a wall-clock ticker
// the probe running last before a cancelled context is an accident of
// scheduling: whichever lands flips the published gauge, and only a
// deterministic shutdown can say which.
func probeLoop(ctx context.Context, address string, forceProtocol int32, ticks <-chan time.Time, log *logging.Logger) {
	for {
		once(ctx, address, forceProtocol, log)
		select {
		case <-ctx.Done():
			return
		case <-ticks:
		}
	}
}

// once is a single probe, published and logged.
//
// Logged at info on success as well as failure. This process does one thing,
// so the log is the record of whether it was doing it — and an outage's
// timeline is built from the first failing line, which only means something
// if the passing ones are there too.
func once(ctx context.Context, address string, forceProtocol int32, log *logging.Logger) {
	result := Probe(ctx, address, forceProtocol)
	Record(result, time.Now())

	if ctx.Err() != nil {
		// Shutdown, not a failure. Publishing it above is still right: the
		// result is real, and the scrape that follows a rolling update should
		// see the last thing this process actually observed.
		return
	}

	fields := logging.Fields{
		"stage":           result.Stage.String(),
		"dialed_protocol": result.DialedProtocol,
		"duration_ms":     result.Duration.Milliseconds(),
	}
	if result.Version != "" {
		fields["server_version"] = result.Version
		fields["server_protocol"] = result.Protocol
	}
	if result.Stage == StageRefused {
		fields["play_status"] = result.PlayStatus
	}
	if result.Err != nil {
		fields["error"] = result.Err.Error()
	}

	if result.Joinable() {
		log.Info("join_probe_ok", fields)
		return
	}
	log.Warn("join_probe_failed", fields)
}
