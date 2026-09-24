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
		rule, err := CheckAllowlist(c.cmd, Kickable{})
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
		if _, err := CheckAllowlist(cmd, Kickable{}); err == nil {
			t.Errorf("CheckAllowlist(%q) = nil error, want refusal", cmd)
		}
	}
}

// testKickable is the actor list the kick tests share: a bare gamertag, one
// with spaces that must travel quoted, and a third so a prefix of one actor
// cannot pass as another.
func testKickable(t *testing.T) Kickable {
	t.Helper()
	k, err := ParseKickable("AfkBotOne,Afk Bot Two,ServerAgent")
	if err != nil {
		t.Fatalf("ParseKickable: %v", err)
	}
	return k
}

func TestCheckAllowlist_KickAllowedForActors(t *testing.T) {
	k := testKickable(t)
	for _, cmd := range []string{
		"kick AfkBotOne",
		"kick afkbotone",
		"kick AFKBOTONE",
		`kick "AfkBotOne"`,
		`kick "Afk Bot Two"`,
		`kick "afk bot two"`,
		"kick ServerAgent",
	} {
		rule, err := CheckAllowlist(cmd, k)
		if err != nil {
			t.Errorf("CheckAllowlist(%q) unexpected error: %v", cmd, err)
			continue
		}
		if rule != "kick" {
			t.Errorf("CheckAllowlist(%q) rule = %q, want kick", cmd, rule)
		}
	}
}

func TestCheckAllowlist_KickRefused(t *testing.T) {
	k := testKickable(t)
	cases := []struct{ cmd, why string }{
		{"kick Steve", "a real player"},
		{`kick "Steve"`, "a real player, quoted"},
		{"kick AfkBot", "a prefix of an actor is not that actor"},
		{"kick AfkBotOne2", "an actor's name plus a suffix is someone else"},
		{"kick AfKBotOne", "the Kelvin sign only Unicode-folds to k"},
		{"kick Afk Bot Two", "unquoted, this is the name Afk plus a reason"},
		{`kick "Afk Bot Two`, "unterminated quote"},
		{`kick " Afk Bot Two"`, "padding inside the quotes changes the name"},
		{`kick "Afk  Bot Two"`, "a doubled inner space changes the name"},
		{"kick AfkBotOne reason text", "a reason argument"},
		{`kick "AfkBotOne" reason`, "a reason after a quoted name"},
		{`kick "AfkBotOne""Steve"`, "a second quoted name"},
		{`kick "AfkBotOne\" Steve"`, "an escaped quote inside the name"},
		{"kick AfkBotOne;stop", "a separator is part of an unknown name"},
		{"kick AfkBotOne ", "trailing space"},
		{"kick  AfkBotOne", "doubled separator"},
		{"kick\tAfkBotOne", "tab separator"},
		{"kick AfkBotOne\nstop", "embedded newline"},
		{"kick AfkBotOne\rstop", "embedded carriage return"},
		{"kick @a", "selector: everyone"},
		{"kick @s", "selector: self"},
		{"kick @r", "selector: random player"},
		{"kick @e[type=player]", "selector with arguments"},
		{"kick @p[name=AfkBotOne]", "a selector naming an actor is still a selector"},
		{`kick "@a"`, "quoted selector"},
		{"kick", "no target"},
		{"kick ", "empty target"},
		{`kick ""`, "empty quoted target"},
		{"Kick AfkBotOne", "templates are case-sensitive"},
	}
	for _, c := range cases {
		if _, err := CheckAllowlist(c.cmd, k); err == nil {
			t.Errorf("CheckAllowlist(%q) = nil error, want refusal (%s)", c.cmd, c.why)
		}
	}
}

func TestCheckAllowlist_KickRefusedWhenNothingIsKickable(t *testing.T) {
	empty, err := ParseKickable("")
	if err != nil {
		t.Fatalf("ParseKickable: %v", err)
	}
	for _, k := range []Kickable{{}, empty} {
		for _, cmd := range []string{"kick AfkBotOne", `kick "Afk Bot Two"`, "kick @a"} {
			if _, err := CheckAllowlist(cmd, k); err == nil {
				t.Errorf("CheckAllowlist(%q) with an empty kickable list = nil error, want refusal", cmd)
			}
		}
	}
}
