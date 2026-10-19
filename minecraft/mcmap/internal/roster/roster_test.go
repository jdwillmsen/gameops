package roster

import (
	"context"
	"os"
	"path/filepath"
	"strconv"
	"testing"
	"time"
)

var t0 = time.Date(2026, 10, 18, 12, 0, 0, 0, time.UTC)

func TestSeenRemembersAPlayerAcrossARestart(t *testing.T) {
	path := filepath.Join(t.TempDir(), "roster", "roster.json")
	r := &Roster{Path: path}
	if err := r.Load(); err != nil {
		t.Fatal(err)
	}
	r.Seen("-42", "Alpha", t0)
	r.Seen("-42", "Alpha", t0.Add(3*time.Minute))
	r.Link("alpha", "2535400000000001")
	if err := r.Save(); err != nil {
		t.Fatal(err)
	}

	again := &Roster{Path: path}
	if err := again.Load(); err != nil {
		t.Fatal(err)
	}
	got := again.List()
	if len(got) != 1 {
		t.Fatalf("got %d players, want 1", len(got))
	}
	e := got[0]
	if e.ID != "-42" || e.Name != "Alpha" || e.XUID != "2535400000000001" || !e.First.Equal(t0) || !e.Last.Equal(t0.Add(3*time.Minute)) {
		t.Fatalf("got %+v", e)
	}
}

func TestSeenKeepsEarlierGamertags(t *testing.T) {
	r := &Roster{}
	r.Seen("7", "Old", t0)
	r.Link("Old", "1")
	r.Seen("7", "New", t0.Add(time.Hour))
	e := r.List()[0]
	if e.Name != "New" || len(e.Earlier) != 1 || e.Earlier[0] != "Old" {
		t.Fatalf("got %+v", e)
	}
	if e.XUID != "" {
		t.Fatalf("an XUID linked by the old name was carried to the new one: %+v", e)
	}
	for i := range 2 * maxEarlier {
		r.Seen("7", "N"+strconv.Itoa(i), t0.Add(time.Duration(i+2)*time.Hour))
	}
	if n := len(r.List()[0].Earlier); n != maxEarlier {
		t.Fatalf("kept %d earlier names, want %d", n, maxEarlier)
	}
}

func TestAGamertagGivenUpDoesNotCarryItsXUIDToTheNextPlayer(t *testing.T) {
	r := &Roster{}
	r.Seen("1", "Taken", t0)
	r.Link("Taken", "500")
	r.Seen("1", "Moved", t0.Add(time.Hour))
	r.Seen("2", "Taken", t0.Add(2*time.Hour))
	for _, e := range r.List() {
		if e.XUID == "500" {
			t.Fatalf("%s was given the XUID of the gamertag's last owner", e.ID)
		}
	}
}

func TestLinkGoesOnlyToTheLatestHolderOfAGamertag(t *testing.T) {
	r := &Roster{}
	r.Seen("1", "Taken", t0)
	r.Seen("2", "Taken", t0.Add(time.Hour))
	r.Link("Taken", "500")
	for _, e := range r.List() {
		if (e.XUID == "500") != (e.ID == "2") {
			t.Fatalf("%s has XUID %q", e.ID, e.XUID)
		}
	}
	r.Seen("1", "Taken", t0.Add(2*time.Hour))
	r.Link("Taken", "700")
	for _, e := range r.List() {
		if (e.XUID == "700") != (e.ID == "1") || e.XUID == "500" {
			t.Fatalf("after 1 took the gamertag back: %s has XUID %q", e.ID, e.XUID)
		}
	}
}

func TestAGamertagGivenUpOfflineDoesNotCarryItsXUIDToTheNextPlayer(t *testing.T) {
	r := &Roster{}
	r.Link("Taken", "500")
	r.Seen("1", "Taken", t0)
	r.Link("Taken", "500")
	r.Seen("2", "Taken", t0.Add(time.Hour))
	for _, e := range r.List() {
		if e.ID == "2" && e.XUID != "" {
			t.Fatalf("the player who took the gamertag was given XUID %q", e.XUID)
		}
	}
	r.Link("Gone", "600")
	r.Seen("3", "Gone", t0.Add(2*time.Hour))
	if x := r.List()[0].XUID; x != "" {
		t.Fatalf("a login before the player was seen linked them to %q", x)
	}
}

func TestSeenRefusesWhatIsNotAPlayer(t *testing.T) {
	r := &Roster{}
	r.Seen("not-an-id", "Alpha", t0)
	r.Seen("1", "", t0)
	r.Seen("2", "bad\nname", t0)
	if n := len(r.List()); n != 0 {
		t.Fatalf("kept %d", n)
	}
}

func TestListIsMostRecentFirst(t *testing.T) {
	r := &Roster{}
	r.Seen("1", "A", t0)
	r.Seen("2", "B", t0.Add(time.Hour))
	r.Seen("3", "C", t0.Add(30*time.Minute))
	got := r.List()
	if got[0].ID != "2" || got[1].ID != "3" || got[2].ID != "1" {
		t.Fatalf("order %v %v %v", got[0].ID, got[1].ID, got[2].ID)
	}
}

func TestAFullRosterDropsThePlayerSeenLongestAgo(t *testing.T) {
	r := &Roster{}
	for i := range MaxPlayers {
		r.Seen(strconv.Itoa(i), "P"+strconv.Itoa(i), t0.Add(time.Duration(i)*time.Minute))
	}
	r.Seen("99999", "Newcomer", t0.Add(48*time.Hour))
	got := r.List()
	if len(got) != MaxPlayers {
		t.Fatalf("kept %d, want %d", len(got), MaxPlayers)
	}
	if got[0].ID != "99999" {
		t.Fatalf("the newcomer was not kept: %+v", got[0])
	}
	for _, e := range got {
		if e.ID == "0" {
			t.Fatal("the player seen longest ago was kept")
		}
	}
}

func TestSaveWritesOnlyWhatChanged(t *testing.T) {
	path := filepath.Join(t.TempDir(), "roster.json")
	r := &Roster{Path: path}
	r.Seen("1", "A", t0)
	if err := r.Save(); err != nil {
		t.Fatal(err)
	}
	before, _ := os.Stat(path)
	if err := os.Chtimes(path, t0, t0); err != nil {
		t.Fatal(err)
	}
	// Seconds later is not worth a write.
	r.Seen("1", "A", t0.Add(5*time.Second))
	if err := r.Save(); err != nil {
		t.Fatal(err)
	}
	after, _ := os.Stat(path)
	if !after.ModTime().Equal(t0) || before.Size() != after.Size() {
		t.Fatal("an unchanged roster was written again")
	}
}

func TestAnUnreadableFileIsSetAside(t *testing.T) {
	path := filepath.Join(t.TempDir(), "roster.json")
	if err := os.WriteFile(path, []byte("{nope"), 0o600); err != nil {
		t.Fatal(err)
	}
	r := &Roster{Path: path}
	if err := r.Load(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(path + ".unreadable"); err != nil {
		t.Fatal("the unreadable file was not kept aside")
	}
	if n := len(r.List()); n != 0 {
		t.Fatalf("kept %d", n)
	}
}

func TestRunSavesOnTheWayOut(t *testing.T) {
	path := filepath.Join(t.TempDir(), "roster.json")
	r := &Roster{Path: path}
	r.Seen("1", "A", t0)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { r.Run(ctx); close(done) }()
	cancel()
	<-done
	if _, err := os.Stat(path); err != nil {
		t.Fatal("not saved on the way out")
	}
}

func TestLastSeenIsSavedWhileAPlayerStaysOnline(t *testing.T) {
	path := filepath.Join(t.TempDir(), "roster.json")
	r := &Roster{Path: path}
	r.Seen("1", "A", t0)
	if err := r.Save(); err != nil {
		t.Fatal(err)
	}
	last := t0
	for i := range 90 {
		last = t0.Add(time.Duration(i+1) * time.Second)
		r.Seen("1", "A", last)
	}
	if err := r.Save(); err != nil {
		t.Fatal(err)
	}
	again := &Roster{Path: path}
	if err := again.Load(); err != nil {
		t.Fatal(err)
	}
	if got := again.List()[0].Last; !got.Equal(last) {
		t.Fatalf("last seen %v after a reload, want %v", got, last)
	}
}
