package main

import (
	"bytes"
	"context"
	"strings"
	"testing"
	"time"

	"github.com/jdwillmsen/gameops/minecraft/crew/internal/arena"
)

var epoch = time.Unix(1_000_000, 0)

// clock is a fake time that only sleeping moves.
type clock struct{ t time.Time }

func (c *clock) now() time.Time { return c.t }
func (c *clock) sleep(_ context.Context, d time.Duration) {
	c.t = c.t.Add(d)
}

func listOf(names ...string) string {
	return "There are " + string(rune('0'+len(names))) + "/10 players online:\n" + strings.Join(names, ", ")
}

// scripted answers each `list` from the script in turn, holding the last one,
// and every gamemode command with success.
func scripted(c *arena.FakeConsole, lists ...string) {
	i := 0
	c.Respond = func(cmd string) (string, error) {
		if cmd == "list" {
			out := lists[min(i, len(lists)-1)]
			i++
			return out, nil
		}
		return "Set own game mode to Creative Mode", nil
	}
}

func harness(c *arena.FakeConsole) (deps, *clock) {
	clk := &clock{t: epoch}
	return deps{
		console: func(string, string) (arena.Console, error) { return c, nil },
		sleep:   clk.sleep,
		now:     clk.now,
	}, clk
}

func runArgs(t *testing.T, c *arena.FakeConsole, args ...string) (int, string, string) {
	t.Helper()
	d, _ := harness(c)
	var out, errb bytes.Buffer
	code := run(context.Background(), args, &out, &errb, d)
	return code, out.String(), errb.String()
}

func gamemodes(c *arena.FakeConsole) []string {
	var got []string
	for _, cmd := range c.Cmds {
		if strings.HasPrefix(cmd, "gamemode") {
			got = append(got, cmd)
		}
	}
	return got
}

func TestOnceSetsCreativeOnlyForNamedUnitsThatAreOnline(t *testing.T) {
	c := &arena.FakeConsole{}
	scripted(c, listOf("Agate", "Steve99"))
	code, _, errs := runArgs(t, c, "--lab", "lab", "--units", "Agate,Basalt", "--once")
	if code != 3 {
		t.Fatalf("exit %d, want 3 (Basalt offline): %s", code, errs)
	}
	if got := gamemodes(c); len(got) != 1 || got[0] != `gamemode creative "Agate"` {
		t.Errorf("gamemode commands = %q", got)
	}
	if !strings.Contains(errs, "Basalt") || strings.Contains(errs, "Steve99") {
		t.Errorf("stderr should name only the unit left in survival: %s", errs)
	}
}

func TestOnceExitsCleanWhenEveryUnitWasSet(t *testing.T) {
	c := &arena.FakeConsole{}
	scripted(c, listOf("Agate", "Basalt"))
	if code, _, errs := runArgs(t, c, "--lab", "lab", "--units", "Agate, Basalt", "--once"); code != 0 {
		t.Fatalf("exit %d: %s", code, errs)
	}
	if n := len(gamemodes(c)); n != 2 {
		t.Errorf("gamemode commands = %d, want 2", n)
	}
}

func TestARejoinIsSetAgainAndAStayIsNot(t *testing.T) {
	c := &arena.FakeConsole{}
	scripted(c,
		listOf("Agate"), listOf("Agate"), // joined, stayed
		listOf(),                         // left
		listOf("Agate"), listOf("Agate"), // came back as a fresh survival player
	)
	code, out, errs := runArgs(t, c, "--lab", "lab", "--units", "Agate", "--poll", "1s", "--max-duration", "5s")
	if code != 0 {
		t.Fatalf("exit %d: %s", code, errs)
	}
	if n := len(gamemodes(c)); n != 2 {
		t.Errorf("gamemode commands = %d, want 2 (one per join): %q", n, c.Cmds)
	}
	if !strings.Contains(out, "stopped (max-duration)") {
		t.Errorf("out = %s", out)
	}
}

func TestAnUnreadableListChangesNothing(t *testing.T) {
	c := &arena.FakeConsole{}
	half := "There are 1/10 players online:"
	scripted(c, half, half, half, listOf("Agate"))
	code, out, _ := runArgs(t, c, "--lab", "lab", "--units", "Agate", "--poll", "1s", "--max-duration", "3s")
	if code != 0 || len(gamemodes(c)) != 1 {
		t.Fatalf("exit %d, gamemodes %q", code, gamemodes(c))
	}
	if !strings.Contains(out, "could not read the player list") {
		t.Errorf("out = %s", out)
	}
}

func TestAUnitThatLeftBeforeTheCommandIsRetried(t *testing.T) {
	c := &arena.FakeConsole{}
	scripted(c, listOf("Agate"))
	tries := 0
	base := c.Respond
	c.Respond = func(cmd string) (string, error) {
		if strings.HasPrefix(cmd, "gamemode") {
			if tries++; tries == 1 {
				return "No targets matched selector", nil
			}
		}
		return base(cmd)
	}
	code, _, _ := runArgs(t, c, "--lab", "lab", "--units", "Agate", "--poll", "1s", "--max-duration", "4s")
	if code != 0 || len(gamemodes(c)) != 2 {
		t.Fatalf("exit %d, gamemodes %q", code, gamemodes(c))
	}
}

func TestMaxCommandsStopsTheWatcher(t *testing.T) {
	c := &arena.FakeConsole{}
	// The unit flaps online and offline, so it would be set on every cycle.
	scripted(c, listOf("Agate"), listOf(), listOf("Agate"), listOf(), listOf("Agate"), listOf(), listOf("Agate"))
	code, out, _ := runArgs(t, c, "--lab", "lab", "--units", "Agate", "--poll", "1s", "--max-duration", "1h", "--max-commands", "2")
	if code != 0 || len(gamemodes(c)) != 2 {
		t.Fatalf("exit %d, gamemodes %q", code, gamemodes(c))
	}
	if !strings.Contains(out, "stopped (max-commands)") {
		t.Errorf("out = %s", out)
	}
}

func TestMaxDurationStopsAnIdleWatcher(t *testing.T) {
	c := &arena.FakeConsole{}
	scripted(c, listOf())
	d, clk := harness(c)
	var out bytes.Buffer
	code := run(context.Background(), []string{"--lab", "lab", "--units", "Agate", "--poll", "3s", "--max-duration", "30s"}, &out, &bytes.Buffer{}, d)
	if code != 0 || clk.t.Sub(epoch) > 30*time.Second {
		t.Fatalf("exit %d after %s", code, clk.t.Sub(epoch))
	}
	if !strings.Contains(out.String(), "stopped (max-duration)") {
		t.Errorf("out = %s", out.String())
	}
}

func TestAnInterruptStopsTheWatcher(t *testing.T) {
	c := &arena.FakeConsole{}
	scripted(c, listOf("Agate"))
	d, _ := harness(c)
	ctx, cancel := context.WithCancel(context.Background())
	d.sleep = func(context.Context, time.Duration) { cancel() }
	var out bytes.Buffer
	if code := run(ctx, []string{"--lab", "lab", "--units", "Agate"}, &out, &bytes.Buffer{}, d); code != 0 || !strings.Contains(out.String(), "stopped (interrupted)") {
		t.Fatalf("exit %d, out %s", code, out.String())
	}
}

func TestRefusals(t *testing.T) {
	fwb := "k8s:jdwillmsen-prd/jdwillmsen-minecraft-fwb-prd-minecraft-bedrock-0"
	for name, args := range map[string][]string{
		"no lab":                  {"--units", "Agate"},
		"no units":                {"--lab", "lab"},
		"fwb":                     {"--lab", fwb, "--units", "Agate"},
		"fwb even when confirmed": {"--lab", fwb, "--confirm-lab", fwb, "--units", "Agate"},
		"unknown compose service": {"--lab", "crew-01", "--units", "Agate"},
		"wrong confirmation":      {"--lab", "k8s:o/p", "--confirm-lab", "k8s:o/q", "--units", "Agate"},
		"selector name":           {"--lab", "lab", "--units", "@a"},
		"quote in name":           {"--lab", "lab", "--units", `A" ; op "x`},
		"name too long":           {"--lab", "lab", "--units", "ABCDEFGHIJKLMNOPQ"},
		"poll too fast":           {"--lab", "lab", "--units", "Agate", "--poll", "10ms"},
		"duration over the cap":   {"--lab", "lab", "--units", "Agate", "--max-duration", "13h"},
		"zero duration":           {"--lab", "lab", "--units", "Agate", "--max-duration", "0s"},
		"zero commands":           {"--lab", "lab", "--units", "Agate", "--max-commands", "0"},
		"unknown flag":            {"--lab", "lab", "--units", "Agate", "--forever"},
		"bad pod target":          {"--lab", "k8s:nopod", "--confirm-lab", "k8s:nopod", "--units", "Agate"},
	} {
		c := &arena.FakeConsole{}
		scripted(c, listOf("Agate"))
		d, _ := harness(c)
		if name == "bad pod target" {
			d.console = arena.NewLabConsole
		}
		if code := run(context.Background(), args, &bytes.Buffer{}, &bytes.Buffer{}, d); code != 2 || len(c.Cmds) != 0 {
			t.Errorf("%s: exit %d, commands %q", name, code, c.Cmds)
		}
	}
}

func TestAllowedTargets(t *testing.T) {
	for name, args := range map[string][]string{
		"compose lab":      {"--lab", "lab"},
		"the crew lab pod": {"--lab", arena.KnownLab},
		"confirmed":        {"--lab", "k8s:o/p", "--confirm-lab", "k8s:o/p"},
	} {
		c := &arena.FakeConsole{}
		scripted(c, listOf("Agate"))
		if code, _, errs := runArgs(t, c, append(args, "--units", "Agate", "--once")...); code != 0 {
			t.Errorf("%s: exit %d: %s", name, code, errs)
		}
	}
}
