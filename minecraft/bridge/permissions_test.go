package main

import (
	"strings"
	"testing"
)

func TestParsePermissions(t *testing.T) {
	in := `[
		{"permission": "operator", "xuid": "2535457893448396"},
		{"permission": "member", "xuid": "2535457893448397"}
	]`
	got, err := ParsePermissions(strings.NewReader(in))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got["2535457893448396"] != "operator" {
		t.Errorf("xuid 396 = %q, want operator", got["2535457893448396"])
	}
	if got["2535457893448397"] != "member" {
		t.Errorf("xuid 397 = %q, want member", got["2535457893448397"])
	}
	if len(got) != 2 {
		t.Errorf("len = %d, want 2", len(got))
	}
}

func TestParsePermissions_Empty(t *testing.T) {
	got, err := ParsePermissions(strings.NewReader(`[]`))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("len = %d, want 0", len(got))
	}
}

func TestParsePermissions_Malformed(t *testing.T) {
	if _, err := ParsePermissions(strings.NewReader(`not json`)); err == nil {
		t.Error("expected error on malformed JSON, got nil")
	}
}

func TestParseAllowlist(t *testing.T) {
	in := `[
		{"name": "Steve", "xuid": "2535457893448396", "ignoresPlayerLimit": false}
	]`
	got, err := ParseAllowlist(strings.NewReader(in))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(got) != 1 || got[0].Name != "Steve" || got[0].XUID != "2535457893448396" {
		t.Errorf("got %+v", got)
	}
}
