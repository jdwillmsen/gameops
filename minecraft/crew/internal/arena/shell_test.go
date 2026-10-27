package arena

import (
	"errors"
	"strings"
	"testing"
	"time"
)

func TestShellConsoleReturnsOnlyNewLogLines(t *testing.T) {
	clock := newFakeClock()
	logs := "server booted\n"
	sent := []string{}
	c := &shellConsole{
		sendArgs: []string{"send-command"},
		logsArgs: []string{"logs"},
		run: func(name string, args []string) ([]byte, error) {
			if name == "send-command" {
				sent = append(sent, args[0])
				if args[0] == "testfor @e[tag=arena_wave]" {
					logs += "Found Zombie\n"
				} else {
					logs += "Found Skeleton\n"
				}
				return nil, nil
			}
			return []byte(logs), nil
		},
		now:   clock.Now,
		sleep: clock.Sleep,
	}
	first, err := c.Exec("testfor @e[tag=arena_wave]")
	if err != nil {
		t.Fatalf("Exec: %v", err)
	}
	if first != "Found Zombie" {
		t.Errorf("first output = %q, want only the new line", first)
	}
	second, err := c.Exec("testfor @e[tag=arena_boss]")
	if err != nil {
		t.Fatalf("Exec: %v", err)
	}
	if second != "Found Skeleton" {
		t.Errorf("second output = %q, want only the new line", second)
	}
	if len(sent) != 2 || sent[0] != "testfor @e[tag=arena_wave]" || sent[1] != "testfor @e[tag=arena_boss]" {
		t.Errorf("sent = %q", sent)
	}
}

func TestShellConsoleRespectsTheCommandRate(t *testing.T) {
	clock := newFakeClock()
	c := &shellConsole{
		sendArgs: []string{"send-command"},
		logsArgs: []string{"logs"},
		run:      func(string, []string) ([]byte, error) { return nil, nil },
		now:      clock.Now,
		sleep:    clock.Sleep,
	}
	start := clock.Now()
	if _, err := c.Exec("one"); err != nil {
		t.Fatal(err)
	}
	if _, err := c.Exec("two"); err != nil {
		t.Fatal(err)
	}
	if elapsed := clock.Now().Sub(start); elapsed < time.Second/commandRate {
		t.Errorf("two commands took %v, want at least %v", elapsed, time.Second/commandRate)
	}
}

func TestShellConsoleResetsOnAShortLog(t *testing.T) {
	clock := newFakeClock()
	logs := "a\nb\nc\n"
	c := &shellConsole{
		sendArgs: []string{"send-command"},
		logsArgs: []string{"logs"},
		run: func(name string, _ []string) ([]byte, error) {
			if name == "logs" {
				return []byte(logs), nil
			}
			return nil, nil
		},
		now:   clock.Now,
		sleep: clock.Sleep,
	}
	if _, err := c.Exec("one"); err != nil {
		t.Fatal(err)
	}
	logs = "fresh\n"
	out, err := c.Exec("two")
	if err != nil {
		t.Fatal(err)
	}
	if out != "fresh" {
		t.Errorf("output = %q after the log shrank", out)
	}
}

func TestShellConsoleReportsFailures(t *testing.T) {
	clock := newFakeClock()
	c := &shellConsole{
		sendArgs: []string{"send-command"},
		logsArgs: []string{"logs"},
		run: func(string, []string) ([]byte, error) {
			return nil, errors.New("no container")
		},
		now:   clock.Now,
		sleep: clock.Sleep,
	}
	if _, err := c.Exec("testfor @e[tag=arena_wave]"); err == nil || !strings.Contains(err.Error(), "no container") {
		t.Errorf("Exec error = %v", err)
	}
}

func TestShellConsoleWaitsPastFillNoiseForTheReply(t *testing.T) {
	clock := newFakeClock()
	logs := "server booted\n"
	polls := 0
	c := &shellConsole{
		sendArgs: []string{"send-command"},
		logsArgs: []string{"logs"},
		run: func(name string, args []string) ([]byte, error) {
			if name == "send-command" {
				logs += "[t] 3 blocks filled\n[t] 1 blocks filled\n"
				return nil, nil
			}
			polls++
			if polls == 3 {
				logs += "[t] Found Rusty\n"
			}
			return []byte(logs), nil
		},
		now:   clock.Now,
		sleep: clock.Sleep,
	}
	out, err := c.Exec("testfor Rusty")
	if err != nil {
		t.Fatalf("Exec: %v", err)
	}
	if n, err := ParseCount(out); err != nil || n != 1 {
		t.Errorf("count = %d, %v from %q, want 1", n, err, out)
	}
}

func TestShellConsoleReturnsFillNoiseWhenNothingElseComes(t *testing.T) {
	clock := newFakeClock()
	logs := ""
	c := &shellConsole{
		sendArgs: []string{"send-command"},
		logsArgs: []string{"logs"},
		run: func(name string, args []string) ([]byte, error) {
			if name == "send-command" {
				logs += "[t] 3 blocks filled\n"
			}
			return []byte(logs), nil
		},
		now:   clock.Now,
		sleep: clock.Sleep,
	}
	out, err := c.Exec("fill 0 0 0 1 1 1 air")
	if err != nil || !strings.Contains(out, "3 blocks filled") {
		t.Errorf("output = %q, %v, want the fill reply", out, err)
	}
}
