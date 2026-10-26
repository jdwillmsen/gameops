package crewexplain

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"regexp"
	"slices"
)

const (
	maxSummary  = 600
	maxUnknown  = 200
	maxUnknowns = 5
	maxEvidence = 20
)

// Draft is a model answer that passed validation.
type Draft struct {
	Cause      string   `json:"cause"`
	Summary    string   `json:"summary"`
	Evidence   []int64  `json:"evidence"`
	Confidence string   `json:"confidence"`
	Unknowns   []string `json:"unknowns"`
}

var (
	// plainText leaves out every character markdown or a shell treats as
	// active, including the slash that starts a game or shell command.
	plainText = regexp.MustCompile(`^[\p{L}\p{N} .,;:'"()\-_%+=!?#]*$`)
	urlLike   = regexp.MustCompile(`(?i)(://|\bwww\.|\bjavascript:|\bdata:)`)
	cmdLike   = regexp.MustCompile(`(?i)\b(sudo|curl|wget|kubectl|chmod|chown|ssh|scp|bash|powershell|eval|exec)\b|\brm\s+-|\bop\s+@|\bgive\s+@`)
)

// ValidateDraft decodes a model reply and enforces what the schema only
// asks for. Any failure discards the draft whole.
func ValidateDraft(raw []byte, w Window) (Draft, error) {
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	var d Draft
	if err := dec.Decode(&d); err != nil {
		return Draft{}, fmt.Errorf("draft is not the expected JSON object: %w", err)
	}
	if _, err := dec.Token(); !errors.Is(err, io.EOF) {
		return Draft{}, errors.New("draft has data after the JSON object")
	}
	if !slices.Contains(Causes, d.Cause) {
		return Draft{}, fmt.Errorf("cause %q is not in the allowed list", d.Cause)
	}
	if !slices.Contains([]string{"low", "medium", "high"}, d.Confidence) {
		return Draft{}, fmt.Errorf("confidence %q is not low, medium or high", d.Confidence)
	}
	if len(d.Evidence) > maxEvidence {
		return Draft{}, fmt.Errorf("%d evidence ids exceed the limit of %d", len(d.Evidence), maxEvidence)
	}
	ids := w.IDs()
	seen := map[int64]bool{}
	for _, id := range d.Evidence {
		if !ids[id] {
			return Draft{}, fmt.Errorf("evidence id %d is not in the window", id)
		}
		if seen[id] {
			return Draft{}, fmt.Errorf("evidence id %d is listed twice", id)
		}
		seen[id] = true
	}
	if d.Cause != CauseNone && d.Cause != CauseUnexplained && len(d.Evidence) == 0 {
		return Draft{}, errors.New("a named cause cites no evidence")
	}
	if len(d.Unknowns) > maxUnknowns {
		return Draft{}, fmt.Errorf("%d unknowns exceed the limit of %d", len(d.Unknowns), maxUnknowns)
	}
	if err := checkText("summary", d.Summary, maxSummary); err != nil {
		return Draft{}, err
	}
	for i, u := range d.Unknowns {
		if err := checkText(fmt.Sprintf("unknowns[%d]", i), u, maxUnknown); err != nil {
			return Draft{}, err
		}
	}
	if d.Unknowns == nil {
		d.Unknowns = []string{}
	}
	if d.Evidence == nil {
		d.Evidence = []int64{}
	}
	return d, nil
}

func checkText(field, s string, limit int) error {
	switch {
	case len([]rune(s)) > limit:
		return fmt.Errorf("%s is longer than %d characters", field, limit)
	case !plainText.MatchString(s):
		return fmt.Errorf("%s has characters outside plain text", field)
	case urlLike.MatchString(s):
		return fmt.Errorf("%s contains a URL", field)
	case cmdLike.MatchString(s):
		return fmt.Errorf("%s contains a command-like string", field)
	}
	return nil
}
