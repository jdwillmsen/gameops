package crewexplain

import (
	"context"
	"fmt"
	"strings"
	"time"
)

// Banner leads every output a draft appears in.
const Banner = "AI draft, unverified"

// Report is one explained window.
type Report struct {
	Banner      string    `json:"banner"`
	WindowID    string    `json:"window_id"`
	From        time.Time `json:"from"`
	To          time.Time `json:"to"`
	Events      int       `json:"events"`
	Generated   time.Time `json:"generated"`
	Model       string    `json:"model,omitempty"`
	ModelReport string    `json:"model_reported,omitempty"`
	PromptHash  string    `json:"prompt_hash"`
	Rules       RuleView  `json:"rule_finding"`
	Draft       *Draft    `json:"draft,omitempty"`
	Disagrees   bool      `json:"disagreement"`
	Note        string    `json:"note,omitempty"`
	index       map[int64]Event
}

// RuleView is the serialisable face of RuleFinding.
type RuleView struct {
	Dominant string   `json:"dominant_cause"`
	Failures int      `json:"failed_tasks"`
	Lines    []string `json:"findings"`
}

// Explain builds the rule finding and, when c is non-nil and there is
// something to explain, asks the model. A draft that fails validation is an
// error and no report is returned: it is discarded, never shown.
func Explain(ctx context.Context, c *Client, w Window, now time.Time) (Report, error) {
	rf := Rules(w)
	r := Report{
		Banner: Banner, WindowID: w.WindowID, From: w.From, To: w.To, Events: len(w.Events), Generated: now.UTC(),
		PromptHash: PromptHash(), index: map[int64]Event{},
		Rules: RuleView{Dominant: rf.Dominant, Failures: rf.Failures, Lines: nonNil(rf.Lines)},
	}
	for _, e := range w.Events {
		r.index[e.ID] = e
	}
	switch {
	case len(w.Events) == 0:
		r.Note = "the window has no events; nothing to explain"
		return r, nil
	case c == nil:
		r.Note = "rules only; no model was asked"
		return r, nil
	case rf.Failures == 0:
		r.Note = "no task failed in the window; the model was not asked"
		return r, nil
	}
	rep, err := c.Ask(ctx, w)
	if err != nil {
		return Report{}, err
	}
	d, err := ValidateDraft([]byte(rep.Content), w)
	if err != nil {
		return Report{}, fmt.Errorf("draft discarded: %w", err)
	}
	r.Model, r.ModelReport = c.Model, rep.Model
	r.Draft = &d
	r.Disagrees = d.Cause != rf.Dominant
	return r, nil
}

func nonNil(s []string) []string {
	if s == nil {
		return []string{}
	}
	return s
}

// Markdown renders the report: banner, header, the rule finding, then the draft.
func (r Report) Markdown() string {
	var b strings.Builder
	fmt.Fprintf(&b, "> **%s.** Written by a language model from the events below. Check every claim against the cited events before acting on it.\n\n", r.Banner)
	fmt.Fprintf(&b, "# Incident summary for window %s\n\n", r.WindowID)
	fmt.Fprintf(&b, "- Window: %s to %s, %d events\n", r.From.UTC().Format(time.RFC3339), r.To.UTC().Format(time.RFC3339), r.Events)
	if r.Model != "" {
		fmt.Fprintf(&b, "- Model: %s", r.Model)
		if r.ModelReport != "" && r.ModelReport != r.Model {
			fmt.Fprintf(&b, " (server reported %s)", r.ModelReport)
		}
		b.WriteString("\n")
	}
	fmt.Fprintf(&b, "- Prompt hash: %s\n- Generated: %s\n\n", r.PromptHash, r.Generated.Format("2006-01-02"))

	b.WriteString("## Rule-based finding\n\n")
	fmt.Fprintf(&b, "Dominant cause: **%s** (%d failed task(s))\n\n", r.Rules.Dominant, r.Rules.Failures)
	for _, l := range r.Rules.Lines {
		fmt.Fprintf(&b, "- %s\n", l)
	}
	if len(r.Rules.Lines) == 0 {
		b.WriteString("- no rule fired\n")
	}
	b.WriteString("\n")

	if r.Draft == nil {
		fmt.Fprintf(&b, "## Draft\n\nNone: %s.\n", r.Note)
		return b.String()
	}
	d := r.Draft
	if r.Disagrees {
		fmt.Fprintf(&b, "**DISAGREEMENT:** the draft names `%s`, the rules name `%s`. Trust the rules until the events show otherwise.\n\n", d.Cause, r.Rules.Dominant)
	}
	fmt.Fprintf(&b, "## Draft (%s)\n\n", strings.ToLower(r.Banner))
	fmt.Fprintf(&b, "- Cause: **%s**\n- Confidence (self-reported, not calibrated): %s\n\n%s\n\n", d.Cause, d.Confidence, d.Summary)
	b.WriteString("### Evidence\n\n")
	for _, id := range d.Evidence {
		e := r.index[id]
		fmt.Fprintf(&b, "- event %d: %s %s %s %s\n", id, e.At.UTC().Format("15:04:05"), e.Unit, e.Kind, e.Reason)
	}
	if len(d.Evidence) == 0 {
		b.WriteString("- none cited\n")
	}
	b.WriteString("\n### Unknowns\n\n")
	for _, u := range d.Unknowns {
		fmt.Fprintf(&b, "- %s\n", u)
	}
	if len(d.Unknowns) == 0 {
		b.WriteString("- none stated\n")
	}
	return b.String()
}
