// Command trainreport summarises a training log of tactics.Scorecard JSONL:
// one row per family × profile with fights, win rate, mean score and the
// 95% interval on it, plus the best profile's per-family progress over time
// buckets. A paired --vs comparison scores two profiles fight-for-fight on
// equal seeds. Aborted fights carry no signal and are skipped; a torn final
// record, from a trainer still writing when it stopped, is tolerated.
//
// usage: trainreport [-json] [-bucket 10m] [-vs A,B] <training.jsonl>
//
// Exit code 0 for a report, 1 for a broken log or unreadable file, 2 for a
// malformed invocation.
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/jdwillmsen/gameops/minecraft/crew/internal/trainreport"
)

func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr))
}

// run is the whole command so the tests can drive it with fixture files and
// buffers instead of exec'ing a binary.
func run(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("trainreport", flag.ContinueOnError)
	fs.SetOutput(stderr)
	jsonOut := fs.Bool("json", false, "machine-readable JSON output")
	bucketDur := fs.Duration("bucket", 10*time.Minute, "progress bucket width, 0 for one bucket")
	vs := fs.String("vs", "", "two profile names A,B to compare pair by seed")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if fs.NArg() != 1 {
		fmt.Fprintln(stderr, "usage: trainreport [-json] [-bucket 10m] [-vs A,B] <training.jsonl>")
		return 2
	}
	fail := func(err error) int {
		fmt.Fprintln(stderr, "trainreport:", err)
		return 1
	}

	cards, torn, err := trainreport.Load(fs.Arg(0))
	if err != nil {
		return fail(err)
	}
	rep := trainreport.Build(cards, trainreport.Options{BucketMs: bucketDur.Milliseconds()})
	rep.TornLines = torn

	var cmp *trainreport.Compare
	if *vs != "" {
		names, err := parseVs(*vs)
		if err != nil {
			return fail(err)
		}
		cmp = trainreport.CompareProfiles(cards, names[0], names[1])
		rep.Compare = cmp
	}

	if *jsonOut {
		data, err := json.MarshalIndent(rep, "", "  ")
		if err != nil {
			return fail(err)
		}
		fmt.Fprintln(stdout, string(data))
		return 0
	}
	trainreport.Render(stdout, rep, cmp)
	return 0
}

// parseVs keeps one name each side of the single comma; anything else is a
// malformed command line, not a data problem.
func parseVs(s string) ([]string, error) {
	parts := strings.Split(s, ",")
	if len(parts) != 2 {
		return nil, fmt.Errorf("--vs wants two comma-joined profile names, got %q", s)
	}
	i, j := strings.TrimSpace(parts[0]), strings.TrimSpace(parts[1])
	if i == "" || j == "" {
		return nil, fmt.Errorf("--vs wants two profile names, got %q", s)
	}
	if i == j {
		return nil, fmt.Errorf("--vs wants two different profiles, got %q twice", s)
	}
	return []string{i, j}, nil
}
