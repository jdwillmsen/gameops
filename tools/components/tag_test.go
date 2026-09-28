package main

import "testing"

func TestParseReleaseTag(t *testing.T) {
	good := map[string][2]string{
		"agent-v0.24.0":      {"agent", "0.24.0"},
		"mcmap-web-v1.2.3":   {"mcmap-web", "1.2.3"},
		"afkbot-v1.0.0-rc.1": {"afkbot", "1.0.0-rc.1"},
		"foo-v2-v3.0.0":      {"foo-v2", "3.0.0"},
	}
	for tag, want := range good {
		p, v, err := ParseReleaseTag(tag)
		if err != nil || p != want[0] || v != want[1] {
			t.Errorf("%s: got %q %q %v, want %q %q", tag, p, v, err, want[0], want[1])
		}
	}
	for _, tag := range []string{"v0.24.0", "agent-0.24.0", "agent-v0.24", "agent-vlatest", "-v1.0.0"} {
		if _, _, err := ParseReleaseTag(tag); err == nil {
			t.Errorf("%s: want error", tag)
		}
	}
}
