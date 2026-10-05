// Command census reads a Bedrock world save and prints a population report.
//
// It runs as a CronJob beside the server rather than inside the agent: the
// scan is a batch job over hundreds of megabytes, and the agent's own pod is
// the one answering players in chat.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/jdwillmsen/gameops/minecraft/agent/internal/census"
)

func main() {
	// A CronJob pod is terminated with SIGTERM, and the extraction and scan
	// together run for minutes. Without this the process dies where it
	// stands, before the deferred cleanup can remove the ~570MB it
	// extracted.
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	if err := run(ctx, os.Args[1:], os.Stdout, os.Stderr); err != nil {
		fmt.Fprintf(os.Stderr, "census: %v\n", err)
		os.Exit(1)
	}
}

// run is the testable body. It writes nothing to stdout unless it produced a
// whole report: a truncated report is worse than none, because it looks like
// an answer.
func run(ctx context.Context, args []string, stdout, stderr io.Writer) error {
	fs := flag.NewFlagSet("census", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	worldDir := fs.String("world-dir", "", "directory another process snapshotted a world into, marked with a snapshot-taken-at file; read in preference to -backup-dir when it holds a world newer than the newest archive")
	backupDir := fs.String("backup-dir", "/backup", "directory holding fwb-<stamp>.tar.gz backup archives; read when -world-dir holds no snapshot or holds an older one")
	topRegions := fs.Int("top-regions", census.DefaultReportOptions().TopRegions, "how many regions to list")
	topTypes := fs.Int("top-types", census.DefaultReportOptions().TopTypes, "how many entity types to list")
	referenceX := fs.Float64("reference-x", census.DefaultReportOptions().Reference.X, "x of the point animal herds are measured from; the default is FWB's base")
	referenceZ := fs.Float64("reference-z", census.DefaultReportOptions().Reference.Z, "z of the point animal herds are measured from; the default is FWB's base")
	metricsFile := fs.String("metrics-file", "", "also write the counts here as a Prometheus text exposition payload; the report on stdout is unchanged either way")
	list := fs.Bool("list", false, "print one JSON object per entity, after a header line, instead of the report")
	types := fs.String("types", "", "with -list, comma-separated entity identifiers to keep, with or without the minecraft: prefix; empty keeps every entity")
	if parseErr := fs.Parse(args); parseErr != nil {
		if errors.Is(parseErr, flag.ErrHelp) {
			// -h/-help is a request for usage, not a failure: it should
			// print to stdout and exit 0 like any other well-behaved CLI.
			fs.SetOutput(stdout)
			fs.PrintDefaults()
			return nil
		}
		return fmt.Errorf("parse flags: %w", parseErr)
	}

	// Refused rather than ignored: a caller that asked for both and got one
	// would find out from a dashboard that stopped moving.
	if *list && *metricsFile != "" {
		return errors.New("-list prints entities and publishes no metrics; run without -list to write -metrics-file")
	}
	if !*list && *types != "" {
		return errors.New("-types filters a listing; add -list, or drop -types for the report")
	}
	// A value that names nothing would otherwise filter nothing, and a
	// caller that meant to narrow a listing would be handed the whole world.
	wantedTypes := splitTypes(*types)
	if *types != "" && len(wantedTypes) == 0 {
		return fmt.Errorf("-types %q names no entity identifier; give at least one, or drop -types to list every entity", *types)
	}

	source, err := chooseSource(*worldDir, *backupDir, stderr)
	if err != nil {
		return err
	}
	if *list {
		return listFrom(ctx, source, wantedTypes, stdout)
	}
	return reportFrom(ctx, source,
		census.ReportOptions{
			TopRegions: *topRegions,
			TopTypes:   *topTypes,
			Reference:  census.Point{X: *referenceX, Z: *referenceZ},
		}, *metricsFile, stdout)
}

// chooseSource assembles where the world comes from.
//
// Both flags set is the normal operating mode rather than a mistake: a
// snapshotter that writes a world when it can get a save hold and nothing
// when it cannot leaves the caller needing somewhere to fall back to. Either
// one alone is a complete configuration too, so all four combinations are
// answered here.
func chooseSource(worldDir, backupDir string, stderr io.Writer) (census.Source, error) {
	switch {
	case worldDir == "" && backupDir == "":
		return nil, errors.New("no world to read: give -world-dir, -backup-dir, or both")
	case worldDir == "":
		return census.ArchiveSource{Dir: backupDir}, nil
	case backupDir == "":
		return census.DirectorySource{Dir: worldDir}, nil
	default:
		return sourceChain{
			snapshot: census.DirectorySource{Dir: worldDir},
			archive:  census.ArchiveSource{Dir: backupDir},
			stderr:   stderr,
		}, nil
	}
}

// sourceChain reads whichever of the two holds the newer world.
//
// Only an absent snapshot falls through. A snapshot that is present but
// malformed fails the run: reading the archive instead would leave a broken
// snapshotter producing plausible reports indefinitely.
type sourceChain struct {
	snapshot census.DirectorySource
	archive  census.ArchiveSource
	stderr   io.Writer
}

func (c sourceChain) Open(ctx context.Context) (census.World, func() error, error) {
	world, cleanup, err := c.snapshot.Open(ctx)
	if err != nil {
		if !errors.Is(err, census.ErrNoSnapshot) {
			return census.World{}, nil, err
		}
		// The report's provenance line will say it read an archive, but not
		// that a fresh snapshot was attempted and missed. That difference is
		// what tells an operator the snapshotter is failing rather than
		// disabled.
		fmt.Fprintf(c.stderr, "census: %v; reading the newest archive instead\n", err)
		return c.archive.Open(ctx)
	}

	// A snapshot is worth preferring only while it is the fresher of the
	// two. One left on a volume that outlived the process that wrote it
	// would otherwise beat last night's backup forever, which is the stale
	// report this source exists to avoid. No archive to compare against is
	// not evidence the snapshot is stale, and the snapshot has already
	// proved itself readable, so the comparison is simply skipped.
	newest, archiveTakenAt, archiveErr := census.NewestArchive(c.archive.Dir)
	if archiveErr != nil || !archiveTakenAt.After(world.TakenAt) {
		return world, cleanup, nil
	}
	if cleanupErr := cleanup(); cleanupErr != nil {
		return census.World{}, nil, fmt.Errorf("release snapshot %s: %w", world.Archive, cleanupErr)
	}
	fmt.Fprintf(c.stderr, "census: snapshot %s is older than archive %s; reading the archive instead\n",
		world.TakenAt.UTC().Format(time.RFC3339), newest)
	return c.archive.Open(ctx)
}

// reportFrom prints the census from an opened source. Splitting it from flag
// parsing is what lets a test supply a source whose cleanup fails.
func reportFrom(ctx context.Context, source census.Source, opts census.ReportOptions, metricsPath string, stdout io.Writer) error {
	return scanFrom(ctx, source, func(world census.World, entities []census.Entity, stats census.ScanStats) error {
		aggregate := census.Aggregate(entities, stats, world.TakenAt, world.Kind)
		if _, err := io.WriteString(stdout, census.Render(aggregate, opts)); err != nil {
			return fmt.Errorf("write report: %w", err)
		}

		// After the report, and only for a run that produced one. Every path
		// that refuses to report also refuses to publish: a payload written
		// from a world this command would not stand behind is worse than a
		// gap in the series, because a graph cannot show the sentence
		// explaining it.
		if metricsPath != "" {
			return census.WriteMetricsFile(metricsPath, aggregate, time.Now(),
				census.MetricsOptions{TopTypes: opts.TopTypes})
		}
		return nil
	})
}

// listFrom prints the per-entity listing from an opened source. It refuses
// the same worlds the report does, for the same reason: a listing of a world
// that did not read is a list of targets that are not there.
func listFrom(ctx context.Context, source census.Source, types []string, stdout io.Writer) error {
	return scanFrom(ctx, source, func(world census.World, entities []census.Entity, stats census.ScanStats) error {
		listing, err := census.RenderListing(entities, stats, world.TakenAt, world.Kind, types)
		if err != nil {
			return err
		}
		if _, err := io.WriteString(stdout, listing); err != nil {
			return fmt.Errorf("write listing: %w", err)
		}
		return nil
	})
}

// splitTypes reads the -types value. A minecraft: prefix is accepted because
// that is how the game and its commands spell an identifier.
func splitTypes(value string) []string {
	var types []string
	for _, t := range strings.Split(value, ",") {
		if t = strings.TrimPrefix(strings.TrimSpace(t), "minecraft:"); t != "" {
			types = append(types, t)
		}
	}
	return types
}

// scanFrom opens a source, scans it, and hands a world worth reporting to
// use. Every refusal lives here so that no output mode can skip one.
func scanFrom(ctx context.Context, source census.Source, use func(census.World, []census.Entity, census.ScanStats) error) (err error) {
	world, cleanup, err := source.Open(ctx)
	if err != nil {
		return err
	}
	defer func() {
		// A silent RemoveAll failure here repeats every scheduled run and
		// slowly fills the volume with ~570MB extractions, so surface it -
		// joined to whatever the run already failed with rather than
		// replacing it, because cancellation is both the likeliest reason
		// the run failed and the case the cleanup exists for.
		if cleanupErr := cleanup(); cleanupErr != nil {
			err = errors.Join(err, fmt.Errorf("clean up extracted world %s: %w", world.Archive, cleanupErr))
		}
	}()

	entities, stats, scanErr := census.Scan(ctx, world.DBPath)
	if scanErr != nil {
		return fmt.Errorf("scan %s %s: %w", world.Kind, world.Archive, scanErr)
	}

	// An archive holding no actor records is a quiet world, which is a fact a
	// census may report. A snapshot holding none is not: it is a copy of a
	// live server's save, taken while that server was running, and a copy
	// that yielded nothing at all stopped before the data did. The ratio
	// check below cannot see this - it weighs records that decoded badly
	// against records seen, and neither exists here.
	if world.Kind == census.KindSnapshot && stats.Records == 0 {
		return fmt.Errorf("snapshot %s holds no actor records at all, which a copy of a live world cannot; it is incomplete", world.Archive)
	}

	// Every section of a report built from records that yielded no entity
	// renders empty, and an empty report reads exactly like a quiet world.
	// Exit non-zero with the counts instead, so the CronJob goes red rather
	// than publishing a world with no mobs in it.
	if stats.Unreadable() {
		unusable := fmt.Errorf("%s %s: %d of %d actor records did not decode into a usable entity (%d unparsable, %d unplaced, %d unidentified), over the %.0f%% limit",
			world.Kind, world.Archive, stats.Unusable(), stats.Records,
			stats.Unparsable, stats.Unplaced, stats.Unidentified, census.MaxUnusableRatio*100)
		if stats.FirstUnparsableErr == "" {
			return unusable
		}
		return fmt.Errorf("%w; first decode failure: %s", unusable, stats.FirstUnparsableErr)
	}

	// The same empty report by another road: every record decoded, and none
	// was claimed by a chunk, so all of them were set aside as orphaned.
	if stats.MostlyOrphaned() {
		return fmt.Errorf("%s %s: %d of %d actor records are orphaned, listed by no chunk, over the %.0f%% limit; the chunk actor lists cannot have been read as the game wrote them (%d bad key, %d bad value)",
			world.Kind, world.Archive, stats.Orphaned, stats.Records, census.MaxOrphanedRatio*100,
			stats.DigpSkippedKey, stats.DigpSkippedValue)
	}

	return use(world, entities, stats)
}
