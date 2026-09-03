package main

import "testing"

func TestCheckAllowlist_Allowed(t *testing.T) {
	cases := []struct {
		cmd  string
		rule string
	}{
		{"list", "list"},
		{"say hello everyone", "say"},
		{"title @a title Welcome", "title"},
		{"title @a[tag=new] actionbar Hi there", "title"},
		{`tellraw @a {"rawtext":[{"text":"hi"}]}`, "tellraw"},
		{`tellraw Steve {"rawtext":[{"text":"hi"}]}`, "tellraw"},
		{"time query day", "time_query"},
		{"time query gametime", "time_query"},
		{"gamerule doDaylightCycle", "gamerule_query"},
	}
	for _, c := range cases {
		rule, err := CheckAllowlist(c.cmd)
		if err != nil {
			t.Errorf("CheckAllowlist(%q) unexpected error: %v", c.cmd, err)
			continue
		}
		if rule != c.rule {
			t.Errorf("CheckAllowlist(%q) rule = %q, want %q", c.cmd, rule, c.rule)
		}
	}
}

func TestCheckAllowlist_Refused(t *testing.T) {
	cases := []string{
		"",
		"stop",
		"kick Steve",
		"gamerule doDaylightCycle false", // write form, not a query
		"say hello\nstop",                // embedded newline command injection
		"say hello\rstop",                // embedded carriage return
		`tellraw @a {"text":"missing rawtext"}`,
		`tellraw @a not-json`,
		"time set day",
		"time query weather", // not a valid query target
		"gamerule ; rm -rf /",
		"list extra-arg",
		"op Steve",
	}
	for _, cmd := range cases {
		if _, err := CheckAllowlist(cmd); err == nil {
			t.Errorf("CheckAllowlist(%q) = nil error, want refusal", cmd)
		}
	}
}
