package arena

import (
	"errors"
	"reflect"
	"strings"
	"testing"
)

func TestOnlineNamesReadsOnlyTheLineAfterTheHeader(t *testing.T) {
	tests := []struct {
		name     string
		out      string
		want     []string
		complete bool
	}{
		{"names", "There are 2/10 players online:\nRusty, Slate", []string{"Rusty", "Slate"}, true},
		{"nobody", "There are 0/10 players online:\n", nil, true},
		{"log prefix on both lines", "[2026-10-23 12:00:00:000 INFO] There are 1/10 players online:\n[2026-10-23 12:00:00:001 INFO] Rusty", []string{"Rusty"}, true},
		{"error line after an empty list", "There are 0/10 players online:\n[2026-10-23 12:00:01:000 ERROR] No targets matched selector", nil, true},
		{"error line before the header", "[2026-10-23 12:00:00:000 ERROR] No targets matched selector\nThere are 1/10 players online:\nRusty", []string{"Rusty"}, true},
		{"warning line is not a player", "There are 0/10 players online:\n[2026-10-23 12:00:01:000 WARN] Slow tick", nil, true},
		{"human among the crew", "There are 2/10 players online:\nRusty, Steve99", []string{"Rusty", "Steve99"}, true},
		{"last header wins", "There are 1/10 players online:\nSteve99\nThere are 0/10 players online:\n", nil, true},
		{"header with the names still unread", "There are 1/10 players online:", nil, false},
		{"no header", "Found Rusty", nil, false},
		{"error line between the header and the names", "There are 1/10 players online:\n[2026-10-23 12:00:01:000 ERROR] No targets matched selector\nSteve99", []string{"Steve99"}, true},
		{"warning line between the header and the names", "There are 2/10 players online:\n[2026-10-23 12:00:01:000 WARN] Slow tick\n[2026-10-23 12:00:01:001 INFO] Rusty, Steve99", []string{"Rusty", "Steve99"}, true},
		{"names wrapped after a comma", "There are 3/10 players online:\nRusty, Slate,\nSteve99", []string{"Rusty", "Slate", "Steve99"}, true},
		{"names wrapped inside a name", "There are 3/10 players online:\nRusty, Sla\nte, Steve99", []string{"Rusty", "Slate", "Steve99"}, true},
		{"fewer names than the count", "There are 2/10 players online:\nRusty", nil, false},
		{"more names than the count", "There are 1/10 players online:\nRusty, Steve99", nil, false},
		{"header without a count", "players online:\nRusty", nil, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, complete := OnlineNames(tt.out)
			if complete != tt.complete || !reflect.DeepEqual(got, tt.want) {
				t.Errorf("OnlineNames = %q, %v; want %q, %v", got, complete, tt.want, tt.complete)
			}
		})
	}
}

func TestNonCrewIgnoresCrewNames(t *testing.T) {
	got := NonCrew([]string{"Rusty", "Probe", "Steve99", "Slate"}, DefaultCrewNames)
	if !reflect.DeepEqual(got, []string{"Steve99"}) {
		t.Errorf("NonCrew = %q, want [Steve99]", got)
	}
	if len(DefaultCrewNames) != 21 {
		t.Errorf("default crew names = %d, want the 20 crew plus Probe", len(DefaultCrewNames))
	}
}

func listConsole(replies ...string) *FakeConsole {
	n := 0
	return &FakeConsole{Respond: func(cmd string) (string, error) {
		if cmd != "list" {
			return "", nil
		}
		r := replies[min(n, len(replies)-1)]
		n++
		return r, nil
	}}
}

func TestCheckAloneRefusesAHuman(t *testing.T) {
	f := listConsole("There are 2/10 players online:\nRusty, Steve99")
	err := CheckAlone(f, DefaultCrewNames)
	var busy *NotAloneError
	if !errors.As(err, &busy) || !reflect.DeepEqual(busy.Names, []string{"Steve99"}) {
		t.Fatalf("CheckAlone = %v, want a refusal naming Steve99", err)
	}
}

func TestCheckAloneIgnoresAnErrorLineFromAnEarlierCommand(t *testing.T) {
	f := listConsole("[2026-10-23 12:00:00:000 ERROR] No targets matched selector\nThere are 1/10 players online:\nRusty\n[2026-10-23 12:00:01:000 ERROR] Unable to summon object")
	if err := CheckAlone(f, DefaultCrewNames); err != nil {
		t.Fatalf("CheckAlone = %v, want the crew alone to pass", err)
	}
	f = listConsole("There are 0/10 players online:\n[2026-10-23 12:00:01:000 ERROR] No targets matched selector")
	if err := CheckAlone(f, DefaultCrewNames); err != nil {
		t.Fatalf("CheckAlone with an empty list = %v, want pass", err)
	}
}

func TestCheckAloneSeesAHumanBehindAnErrorLine(t *testing.T) {
	f := listConsole("There are 2/10 players online:\n[2026-10-23 12:00:01:000 ERROR] No targets matched selector\nRusty, Steve99")
	var busy *NotAloneError
	if err := CheckAlone(f, DefaultCrewNames); !errors.As(err, &busy) || !reflect.DeepEqual(busy.Names, []string{"Steve99"}) {
		t.Fatalf("CheckAlone = %v, want a refusal naming Steve99", err)
	}
}

func TestCheckAloneRefusesALastNameCutByAWrap(t *testing.T) {
	// The count is already met at the cut, so the fragment is read as a
	// name; it is not crew, which refuses rather than passes.
	f := listConsole("There are 2/10 players online:\nRusty, Sla\nte")
	var busy *NotAloneError
	if err := CheckAlone(f, DefaultCrewNames); !errors.As(err, &busy) {
		t.Fatalf("CheckAlone = %v, want a refusal", err)
	}
}

func TestCheckAloneFailsClosedWhenTheCountDoesNotMatch(t *testing.T) {
	f := listConsole("There are 2/10 players online:\nRusty")
	err := CheckAlone(f, DefaultCrewNames)
	if err == nil {
		t.Fatal("CheckAlone passed a list missing one of its two players")
	}
	if f.Count("list") != listAttempts {
		t.Errorf("list sent %d times, want %d rereads before giving up", f.Count("list"), listAttempts)
	}
}

func TestCheckAloneRereadsAListWhoseNamesHadNotArrived(t *testing.T) {
	f := listConsole("There are 1/10 players online:", "Steve99\nThere are 1/10 players online:")
	var busy *NotAloneError
	if err := CheckAlone(f, DefaultCrewNames); !errors.As(err, &busy) {
		t.Fatalf("CheckAlone = %v, want the late names line to count", err)
	}
	if f.Count("list") != 2 {
		t.Errorf("list sent %d times, want 2", f.Count("list"))
	}
}

func TestCheckAloneFailsClosedOnAnUnreadableList(t *testing.T) {
	f := listConsole("")
	err := CheckAlone(f, DefaultCrewNames)
	var busy *NotAloneError
	if err == nil || errors.As(err, &busy) || !strings.Contains(err.Error(), "player list") {
		t.Fatalf("CheckAlone = %v, want a read error that is not a refusal", err)
	}
}
