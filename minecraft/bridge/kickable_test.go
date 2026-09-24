package main

import "testing"

func TestParseKickable(t *testing.T) {
	k, err := ParseKickable(" AfkBotOne, Afk Bot Two ,ServerAgent,")
	if err != nil {
		t.Fatalf("ParseKickable: %v", err)
	}
	if k.Len() != 3 {
		t.Errorf("Len = %d, want 3", k.Len())
	}
	for _, name := range []string{"AfkBotOne", "afkbotone", "AFKBOTONE", "Afk Bot Two", "afk bot two", "ServerAgent"} {
		if !k.Contains(name) {
			t.Errorf("Contains(%q) = false, want true", name)
		}
	}
	for _, name := range []string{"", "Steve", "AfkBot", "AfkBotOne2", " AfkBotOne", "Afk  Bot Two", "AfkBotTwo"} {
		if k.Contains(name) {
			t.Errorf("Contains(%q) = true, want false", name)
		}
	}
}

// Unicode case folding maps the Kelvin sign to "k", so a folding match would
// accept a name the operator never listed. Only ASCII case is ignored.
func TestKickableContains_IgnoresOnlyASCIICase(t *testing.T) {
	k, err := ParseKickable("AfkBotOne")
	if err != nil {
		t.Fatalf("ParseKickable: %v", err)
	}
	if k.Contains("AfKBotOne") {
		t.Error(`Contains("AfKBotOne") = true, want false: the Kelvin sign is not the letter k`)
	}
}

func TestParseKickable_EmptyRefusesEveryone(t *testing.T) {
	for _, raw := range []string{"", " ", ",", " , ,"} {
		k, err := ParseKickable(raw)
		if err != nil {
			t.Fatalf("ParseKickable(%q): %v", raw, err)
		}
		if k.Len() != 0 {
			t.Errorf("ParseKickable(%q).Len = %d, want 0", raw, k.Len())
		}
		if k.Contains("AfkBotOne") {
			t.Errorf("ParseKickable(%q) made AfkBotOne kickable", raw)
		}
	}
	if (Kickable{}).Contains("AfkBotOne") {
		t.Error("the zero Kickable made AfkBotOne kickable")
	}
}

func TestParseKickable_DuplicatesCollapse(t *testing.T) {
	k, err := ParseKickable("AfkBotOne,afkbotone")
	if err != nil {
		t.Fatalf("ParseKickable: %v", err)
	}
	if k.Len() != 1 {
		t.Errorf("Len = %d, want 1", k.Len())
	}
}

// An entry that could change how the console parses `kick "<name>"` is a
// startup error, not a silently dropped name: a quote or backslash could end
// the quoted name early, a leading @ is a selector, and a control character
// has no place in a gamertag.
func TestParseKickable_UnsafeEntryIsAnError(t *testing.T) {
	for _, raw := range []string{`Afk"Bot`, `Afk\Bot`, "@a", "@p[name=Steve]", "Afk\tBot", "AfkBot\x00"} {
		if _, err := ParseKickable("AfkBotOne," + raw); err == nil {
			t.Errorf("ParseKickable with entry %q returned no error", raw)
		}
	}
}
