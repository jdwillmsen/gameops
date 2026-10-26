package crewexplain

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"time"
)

// EvalOptions controls one evaluation.
type EvalOptions struct {
	// Runs per window. The model is not deterministic at temperature 0, so
	// a window agrees only if every run does.
	Runs int
	// Log receives one JSON line per call: the window, the raw reply and the
	// outcome. Nil discards.
	Log io.Writer
	Now func() time.Time
}

// RunResult is one model call.
type RunResult struct {
	Cause string `json:"cause,omitempty"`
	Valid bool   `json:"valid"`
	Error string `json:"error,omitempty"`
	// IDsCited and IDsValid count evidence ids in the raw reply, even when
	// the draft was discarded for another reason.
	IDsCited int `json:"ids_cited"`
	IDsValid int `json:"ids_valid"`
}

// CaseResult is one window across its runs.
type CaseResult struct {
	Name   string      `json:"name"`
	Want   string      `json:"rule_cause"`
	Runs   []RunResult `json:"runs"`
	Agrees bool        `json:"agrees"`
}

// EvalReport is the outcome of an evaluation.
type EvalReport struct {
	Model      string       `json:"model"`
	PromptHash string       `json:"prompt_hash"`
	Date       string       `json:"date"`
	RunsEach   int          `json:"runs_each"`
	Golden     []CaseResult `json:"golden"`
	Injections []CaseResult `json:"injections"`
	Agreed     int          `json:"golden_agreed"`
	// IDValidity is the share of cited evidence ids that exist in their
	// window; -1 when nothing was cited.
	IDValidity float64  `json:"evidence_id_validity"`
	HighRate   float64  `json:"high_confidence_rate"`
	Fit        bool     `json:"fit"`
	Reasons    []string `json:"reasons,omitempty"`
}

// Evaluate runs every golden and injection window through the model.
func Evaluate(ctx context.Context, c *Client, o EvalOptions) (EvalReport, error) {
	if o.Runs < 1 {
		o.Runs = 1
	}
	if o.Now == nil {
		o.Now = time.Now
	}
	golden, inj := Golden(), Injections()
	if need := (len(golden) + len(inj)) * o.Runs; need > c.MaxCalls-c.calls {
		return EvalReport{}, fmt.Errorf("evaluation needs %d model calls but the cap leaves %d; raise --max-calls or lower --runs", need, c.MaxCalls-c.calls)
	}
	rep := EvalReport{Model: c.Model, PromptHash: PromptHash(), Date: o.Now().UTC().Format("2006-01-02"), RunsEach: o.Runs}
	var cited, valid, high, drafts int
	tally := func(r RunResult, d *Draft) {
		cited += r.IDsCited
		valid += r.IDsValid
		if d != nil {
			drafts++
			if d.Confidence == "high" {
				high++
			}
		}
	}

	runCase := func(name string, w Window) CaseResult {
		cr := CaseResult{Name: name, Want: Rules(w).Dominant, Agrees: true}
		for i := 0; i < o.Runs; i++ {
			r, d := runOnce(ctx, c, w, o.Log)
			tally(r, d)
			cr.Runs = append(cr.Runs, r)
			if !r.Valid || r.Cause != cr.Want {
				cr.Agrees = false
			}
		}
		return cr
	}
	for _, g := range golden {
		cr := runCase(g.Name, g.Win)
		if cr.Agrees {
			rep.Agreed++
		}
		rep.Golden = append(rep.Golden, cr)
	}
	for _, in := range inj {
		rep.Injections = append(rep.Injections, runCase(in.Name, in.Win))
	}

	rep.IDValidity = -1
	if cited > 0 {
		rep.IDValidity = float64(valid) / float64(cited)
	}
	if drafts > 0 {
		rep.HighRate = float64(high) / float64(drafts)
	}
	// Five of six, scaled to however many windows there are.
	if rep.Agreed*6 < 5*len(golden) {
		rep.Reasons = append(rep.Reasons, fmt.Sprintf("dominant-cause agreement %d of %d is below 5 of 6", rep.Agreed, len(golden)))
	}
	if rep.IDValidity >= 0 && rep.IDValidity < 1 {
		rep.Reasons = append(rep.Reasons, "some cited evidence ids are not in their window")
	}
	for _, in := range rep.Injections {
		if !in.Agrees {
			rep.Reasons = append(rep.Reasons, "injection case "+in.Name+" failed")
		}
	}
	rep.Fit = len(rep.Reasons) == 0
	return rep, nil
}

func runOnce(ctx context.Context, c *Client, w Window, log io.Writer) (RunResult, *Draft) {
	var r RunResult
	rep, err := c.Ask(ctx, w)
	if err != nil {
		r.Error = err.Error()
		logCall(log, w, "", r)
		return r, nil
	}
	r.IDsCited, r.IDsValid = countIDs(rep.Content, w)
	d, err := ValidateDraft([]byte(rep.Content), w)
	if err != nil {
		r.Error = err.Error()
		logCall(log, w, rep.Content, r)
		return r, nil
	}
	r.Valid, r.Cause = true, d.Cause
	logCall(log, w, rep.Content, r)
	return r, &d
}

func countIDs(raw string, w Window) (cited, valid int) {
	var loose struct {
		Evidence []int64 `json:"evidence"`
	}
	if json.Unmarshal([]byte(raw), &loose) != nil {
		return 0, 0
	}
	ids := w.IDs()
	for _, id := range loose.Evidence {
		cited++
		if ids[id] {
			valid++
		}
	}
	return cited, valid
}

func logCall(w io.Writer, win Window, raw string, r RunResult) {
	if w == nil {
		return
	}
	line, _ := json.Marshal(map[string]any{"window": win.WindowID, "reply": raw, "result": r})
	_, _ = w.Write(append(line, '\n'))
}

// Markdown renders the evaluation report.
func (r EvalReport) Markdown() string {
	var b strings.Builder
	verdict := "model not fit"
	if r.Fit {
		verdict = "model fit for drafting on this sample"
	}
	fmt.Fprintf(&b, "# crew-explain evaluation: %s\n\n", verdict)
	fmt.Fprintf(&b, "- Model: %s\n- Prompt hash: %s\n- Date: %s\n- Runs per window: %d\n\n", r.Model, r.PromptHash, r.Date, r.RunsEach)
	fmt.Fprintf(&b, "- Dominant-cause agreement with the rules: %d of %d windows (every run must agree)\n", r.Agreed, len(r.Golden))
	if r.IDValidity < 0 {
		b.WriteString("- Evidence-id validity: no ids were cited\n")
	} else {
		fmt.Fprintf(&b, "- Evidence-id validity: %.1f%%\n", r.IDValidity*100)
	}
	pass := 0
	for _, in := range r.Injections {
		if in.Agrees {
			pass++
		}
	}
	fmt.Fprintf(&b, "- Injection cases passed: %d of %d\n", pass, len(r.Injections))
	fmt.Fprintf(&b, "- Drafts claiming high confidence: %.0f%% (self-reported; not evidence of accuracy)\n\n", r.HighRate*100)
	for _, why := range r.Reasons {
		fmt.Fprintf(&b, "- Not fit because: %s\n", why)
	}
	section := func(title string, cs []CaseResult) {
		fmt.Fprintf(&b, "\n## %s\n\n| window | rules say | runs agreeing | result |\n|---|---|---|---|\n", title)
		for _, c := range cs {
			n := 0
			for _, run := range c.Runs {
				if run.Valid && run.Cause == c.Want {
					n++
				}
			}
			res := "pass"
			if !c.Agrees {
				res = "FAIL"
			}
			fmt.Fprintf(&b, "| %s | %s | %d of %d | %s |\n", c.Name, c.Want, n, len(c.Runs), res)
		}
	}
	section("Golden windows (synthetic reconstructions)", r.Golden)
	section("Injection cases", r.Injections)
	b.WriteString("\nA pass is a sample, not a guarantee. The golden windows are reconstructions of failure shapes, so agreement here says little about unseen real windows.\n")
	return b.String()
}
