// Command crew-explain drafts an incident summary of an exported event
// window with a local model, and evaluates whether that model deserves to be
// trusted for it. It is an operator tool run on their own machine: it is not
// part of any image, holds no credentials, and writes only the output file.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"strings"
	"time"

	"github.com/jdwillmsen/gameops/minecraft/crew-control/internal/crewexplain"
)

const usage = `usage:
  crew-explain [flags] <window.json>   draft a summary of an exported window
  crew-explain eval [flags]            run the golden and injection windows through the model

flags:
  --base-url URL     OpenAI-compatible endpoint (default http://127.0.0.1:8000/v1)
  --model ID         model id to ask for (default local-chat)
  --allow-host HOST  allow a non-loopback host, repeatable; loopback is always allowed
  --format md|json   output format (default md)
  --out FILE         write the output here instead of stdout
  --timeout D        per-call timeout (default 2m)
  --max-tokens N     answer cap (default 700)
  --max-calls N      model calls allowed in this run (default 60)
  --rules-only       print the rule-based finding without asking a model
eval flags:
  --runs N           runs per window (default 3)
  --log FILE         write every prompt reply to this file for review

exit status: 0 on success (eval: model fit), 1 otherwise; the reason is on stderr.
`

type hostList []string

func (h *hostList) String() string     { return strings.Join(*h, ",") }
func (h *hostList) Set(v string) error { *h = append(*h, v); return nil }

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	os.Exit(run(ctx, os.Args[1:], os.Stdout, os.Stderr, time.Now))
}

func run(ctx context.Context, args []string, stdout, stderr io.Writer, now func() time.Time) int {
	fail := func(err error) int {
		fmt.Fprintf(stderr, "error: %v\n", err)
		return 1
	}
	evalMode := len(args) > 0 && args[0] == "eval"
	if evalMode {
		args = args[1:]
	}
	fs := flag.NewFlagSet("crew-explain", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	baseURL := fs.String("base-url", "http://127.0.0.1:8000/v1", "")
	model := fs.String("model", "local-chat", "")
	format := fs.String("format", "md", "")
	out := fs.String("out", "", "")
	timeout := fs.Duration("timeout", 2*time.Minute, "")
	maxTokens := fs.Int("max-tokens", crewexplain.DefaultMaxTokens, "")
	maxCalls := fs.Int("max-calls", crewexplain.DefaultMaxCalls, "")
	rulesOnly := fs.Bool("rules-only", false, "")
	runs := fs.Int("runs", 3, "")
	logPath := fs.String("log", "", "")
	var hosts hostList
	fs.Var(&hosts, "allow-host", "")

	// Flags may follow the file name.
	var files []string
	for rest := args; ; {
		if err := fs.Parse(rest); err != nil {
			if errors.Is(err, flag.ErrHelp) {
				fmt.Fprint(stdout, usage)
				return 0
			}
			fmt.Fprint(stderr, usage)
			return fail(err)
		}
		rest = fs.Args()
		if len(rest) == 0 {
			break
		}
		files = append(files, rest[0])
		rest = rest[1:]
	}
	if *format != "md" && *format != "json" {
		return fail(fmt.Errorf("--format must be md or json, not %q", *format))
	}
	if evalMode && (len(files) != 0 || *rulesOnly) {
		return fail(errors.New("eval takes no window file and no --rules-only"))
	}
	if !evalMode && len(files) != 1 {
		fmt.Fprint(stderr, usage)
		return fail(errors.New("exactly one window file is required"))
	}

	var client *crewexplain.Client
	if !*rulesOnly {
		c, err := crewexplain.NewClient(*baseURL, *model, hosts, *timeout)
		if err != nil {
			return fail(err)
		}
		c.MaxTokens, c.MaxCalls = *maxTokens, *maxCalls
		client = c
	}

	var text string
	code := 0
	if evalMode {
		var logw io.Writer
		var logf *os.File
		if *logPath != "" {
			f, err := os.OpenFile(*logPath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600)
			if err != nil {
				return fail(err)
			}
			logf, logw = f, f
		}
		rep, err := crewexplain.Evaluate(ctx, client, crewexplain.EvalOptions{Runs: *runs, Log: logw, Now: now})
		// A failed close can drop buffered log lines, so it fails the run like a write error.
		if logf != nil {
			if cerr := logf.Close(); err == nil {
				err = cerr
			}
		}
		if err != nil {
			return fail(err)
		}
		if text, err = render(*format, rep, rep.Markdown()); err != nil {
			return fail(err)
		}
		if !rep.Fit {
			code = 1
		}
	} else {
		w, err := crewexplain.ReadWindow(files[0])
		if err != nil {
			return fail(err)
		}
		rep, err := crewexplain.Explain(ctx, client, w, now())
		if err != nil {
			return fail(err)
		}
		if text, err = render(*format, rep, rep.Markdown()); err != nil {
			return fail(err)
		}
	}

	if *out != "" {
		if err := os.WriteFile(*out, []byte(text), 0o600); err != nil {
			return fail(err)
		}
		return code
	}
	fmt.Fprint(stdout, text)
	return code
}

func render(format string, v any, md string) (string, error) {
	if format == "md" {
		return md, nil
	}
	b, err := json.MarshalIndent(v, "", "  ")
	return string(b) + "\n", err
}
