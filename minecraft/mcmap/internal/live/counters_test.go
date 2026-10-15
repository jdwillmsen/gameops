package live

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

// Every id, type and amount here is invented.
func countsLine(boot, seq int, items string) string {
	return fmt.Sprintf(`{"gen":7,"tick":100,"ts":1790000000000,"kind":"counts","boot":%d,"seq":%d,"items":[%s]}`, boot, seq, items)
}

const (
	alexDelta = `{"i":"-11","d":{"fall":1},"k":{"zombie":2,"spider":1},"b":5,"bt":{"stone":4,"dirt":1},"p":3,"pt":{"oak_planks":3},"j":1,"l":1,"t":90}`
	boDelta   = `{"i":"-22","b":1,"bt":{"stone":1}}`
)

var epoch = time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC)

func ingest(l *Layer, line string, at time.Time) {
	l.Ingest(RawRecord{ID: 1, At: at, Data: line}, at)
}

func newCounted(t *testing.T) (*Layer, *Counters) {
	t.Helper()
	c := &Counters{Path: filepath.Join(t.TempDir(), "counters", "counters.json")}
	if err := c.Load(); err != nil {
		t.Fatal(err)
	}
	l := New(time.Minute, 100, nil)
	l.Counters = c
	return l, c
}

func TestParseReadsACountsRecord(t *testing.T) {
	rec, _, err := Parse(countsLine(5, 3, alexDelta+","+boDelta))
	if err != nil {
		t.Fatal(err)
	}
	if rec.Kind != Counts || rec.Boot != 5 || rec.Seq != 3 || len(rec.Deltas) != 2 {
		t.Fatalf("record = %+v", rec)
	}
	d := rec.Deltas[0]
	if d.ID != "-11" || d.Deaths["fall"] != 1 || d.Kills["zombie"] != 2 || d.Broken != 5 || d.BrokenTypes["stone"] != 4 || d.Placed != 3 || d.Joins != 1 || d.Leaves != 1 || d.OnlineSeconds != 90 {
		t.Errorf("delta = %+v", d)
	}
}

func TestParseRefusesACountsRecordItCannotTrust(t *testing.T) {
	for name, line := range map[string]string{
		"no boot":        `{"gen":1,"kind":"counts","seq":1,"items":[]}`,
		"no seq":         `{"gen":1,"kind":"counts","boot":1,"items":[]}`,
		"no id":          countsLine(1, 1, `{"b":1}`),
		"negative":       countsLine(1, 1, `{"i":"1","b":-1}`),
		"absurd":         countsLine(1, 1, `{"i":"1","b":9999999999}`),
		"negative type":  countsLine(1, 1, `{"i":"1","k":{"cow":-3}}`),
		"unnamed type":   countsLine(1, 1, `{"i":"1","k":{"":3}}`),
		"items not list": `{"gen":1,"kind":"counts","boot":1,"seq":1,"items":{}}`,
	} {
		if _, _, err := Parse(line); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
	var many []string
	for i := 0; i <= maxDeltaTypes; i++ {
		many = append(many, fmt.Sprintf(`"t%d":1`, i))
	}
	if _, _, err := Parse(countsLine(1, 1, `{"i":"1","k":{`+strings.Join(many, ",")+`}}`)); err == nil {
		t.Error("accepted more types than a record may name")
	}
}

// A map from before counts existed meets lines of a kind it has never heard
// of, and today's map meets the next kind in turn. Either must change
// nothing it draws.
func TestAnUnknownKindIsRefusedAndTouchesNothing(t *testing.T) {
	l, c := newCounted(t)
	ingest(l, playersLine, epoch)
	before := l.Store.Snapshot("overworld", epoch)

	ingest(l, `{"gen":8,"kind":"achievements","boot":1,"seq":1,"items":[{"i":"-11","b":1}]}`, epoch)
	if _, _, err := Parse(`{"gen":8,"kind":"achievements","items":[]}`); err == nil {
		t.Error("an unknown kind was accepted")
	}

	after := l.Store.Snapshot("overworld", epoch)
	if len(after.Players) != len(before.Players) || after.Players[0].ID != before.Players[0].ID {
		t.Errorf("the picture changed: %+v then %+v", before, after)
	}
	if _, ok := c.Lookup("-11"); ok {
		t.Error("an unknown kind was counted")
	}
}

func TestPlayerRecordsReadVitalsAndIgnoreThemWhenAbsentOrNonsense(t *testing.T) {
	rec, _, err := Parse(`{"gen":1,"kind":"players","dim":"overworld","part":0,"parts":1,"more":0,"items":[` +
		`{"i":"1","x":0,"y":0,"z":0,"h":17,"hm":20,"xl":12},` +
		`{"i":"2","x":0,"y":0,"z":0},` +
		`{"i":"3","x":0,"y":0,"z":0,"h":-4,"hm":0,"xl":-1}]}`)
	if err != nil {
		t.Fatal(err)
	}
	a, b, c := rec.Items[0], rec.Items[1], rec.Items[2]
	if a.Health == nil || *a.Health != 17 || *a.MaxHealth != 20 || a.Level == nil || *a.Level != 12 {
		t.Errorf("vitals = %+v", a)
	}
	if b.Health != nil || b.Level != nil || c.Health != nil || c.MaxHealth != nil || c.Level != nil {
		t.Errorf("vitals that were absent or nonsense were kept: %+v %+v", b, c)
	}
}

func TestCountsAddUpPerPlayer(t *testing.T) {
	l, c := newCounted(t)
	ingest(l, countsLine(5, 1, alexDelta+","+boDelta), epoch)
	ingest(l, countsLine(5, 2, alexDelta), epoch.Add(time.Minute))

	alex, ok := c.Lookup("-11")
	if !ok {
		t.Fatal("no totals for the player")
	}
	if alex.Kills["zombie"] != 4 || alex.Kills["spider"] != 2 || alex.Deaths["fall"] != 2 || alex.Broken != 10 || alex.BrokenTypes["stone"] != 8 ||
		alex.Placed != 6 || alex.PlacedTypes["oak_planks"] != 6 || alex.Joins != 2 || alex.Leaves != 2 || alex.OnlineSeconds != 180 {
		t.Errorf("totals = %+v", alex)
	}
	if !alex.Since.Equal(epoch) {
		t.Errorf("counting since %v, want %v", alex.Since, epoch)
	}
	if bo, _ := c.Lookup("-22"); bo.Broken != 1 || !bo.Since.Equal(epoch) {
		t.Errorf("another player's totals = %+v", bo)
	}
	if _, ok := c.Lookup("-33"); ok {
		t.Error("totals for a player nobody counted")
	}
}

func TestALineDeliveredAgainIsNotCountedAgain(t *testing.T) {
	l, c := newCounted(t)
	for i := 0; i < 3; i++ {
		ingest(l, countsLine(5, 1, boDelta), epoch)
	}
	// The bridge answering a later poll with the same ring plus a new line.
	ingest(l, countsLine(5, 1, boDelta), epoch)
	ingest(l, countsLine(5, 2, boDelta), epoch)
	ingest(l, countsLine(5, 2, boDelta), epoch)
	if bo, _ := c.Lookup("-22"); bo.Broken != 2 {
		t.Errorf("broken = %d, want 2", bo.Broken)
	}
}

func TestARestartedPackIsNewAndAnOldRunIsStillRemembered(t *testing.T) {
	l, c := newCounted(t)
	ingest(l, countsLine(5, 1, boDelta), epoch)
	ingest(l, countsLine(5, 2, boDelta), epoch)
	// The script reloaded: a new boot starts its sequence over.
	ingest(l, countsLine(9, 1, boDelta), epoch)
	// The ring still holds lines of the run before it.
	ingest(l, countsLine(5, 2, boDelta), epoch)
	ingest(l, countsLine(9, 1, boDelta), epoch)
	if bo, _ := c.Lookup("-22"); bo.Broken != 3 {
		t.Errorf("broken = %d, want 3", bo.Broken)
	}
}

func TestAClockThatWentBackwardsDoesNotSilenceANewRun(t *testing.T) {
	l, c := newCounted(t)
	ingest(l, countsLine(900, 4, boDelta), epoch)
	ingest(l, countsLine(100, 1, boDelta), epoch)
	if bo, _ := c.Lookup("-22"); bo.Broken != 2 {
		t.Errorf("broken = %d, want 2", bo.Broken)
	}
}

// The failure that matters: the map restarts and the bridge hands it the
// lines it already counted, along with ones it never saw.
func TestARestartedMapDoesNotRecountWhatItSaved(t *testing.T) {
	l, c := newCounted(t)
	ingest(l, countsLine(5, 1, boDelta), epoch)
	ingest(l, countsLine(5, 2, boDelta), epoch)
	if err := c.Save(); err != nil {
		t.Fatal(err)
	}

	again := &Counters{Path: c.Path}
	if err := again.Load(); err != nil {
		t.Fatal(err)
	}
	l2 := New(time.Minute, 100, nil)
	l2.Counters = again
	for seq := 1; seq <= 3; seq++ {
		ingest(l2, countsLine(5, seq, boDelta), epoch.Add(time.Hour))
	}
	bo, _ := again.Lookup("-22")
	if bo.Broken != 3 {
		t.Errorf("broken = %d, want 3", bo.Broken)
	}
	if !bo.Since.Equal(epoch) {
		t.Errorf("counting since %v after a restart, want %v", bo.Since, epoch)
	}
}

// Lines taken after the last save are not in the file, and neither is the
// number saying they were: the bridge's replay puts them back.
func TestLinesAfterTheLastSaveComeBackFromTheBridge(t *testing.T) {
	l, c := newCounted(t)
	ingest(l, countsLine(5, 1, boDelta), epoch)
	if err := c.Save(); err != nil {
		t.Fatal(err)
	}
	ingest(l, countsLine(5, 2, boDelta), epoch)

	again := &Counters{Path: c.Path}
	if err := again.Load(); err != nil {
		t.Fatal(err)
	}
	l2 := New(time.Minute, 100, nil)
	l2.Counters = again
	ingest(l2, countsLine(5, 1, boDelta), epoch)
	ingest(l2, countsLine(5, 2, boDelta), epoch)
	if bo, _ := again.Lookup("-22"); bo.Broken != 2 {
		t.Errorf("broken = %d, want 2", bo.Broken)
	}
}

func TestSaveIsAtomicAndLeavesNoTemporaryFile(t *testing.T) {
	l, c := newCounted(t)
	ingest(l, countsLine(5, 1, boDelta), epoch)
	if err := c.Save(); err != nil {
		t.Fatal(err)
	}
	first, err := os.ReadFile(c.Path)
	if err != nil {
		t.Fatal(err)
	}
	// Nothing changed, so nothing is rewritten.
	if err := os.Remove(c.Path); err != nil {
		t.Fatal(err)
	}
	if err := c.Save(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(c.Path); err == nil {
		t.Error("an unchanged table was written again")
	}
	ingest(l, countsLine(5, 2, boDelta), epoch)
	if err := c.Save(); err != nil {
		t.Fatal(err)
	}
	second, _ := os.ReadFile(c.Path)
	if string(first) == string(second) {
		t.Error("the file did not change")
	}
	entries, _ := os.ReadDir(filepath.Dir(c.Path))
	if len(entries) != 1 {
		t.Errorf("the directory holds %d files, want only the counters", len(entries))
	}
	for _, banned := range []string{"gamertag", "xuid"} {
		if strings.Contains(strings.ToLower(string(second)), banned) {
			t.Errorf("the file holds %q", banned)
		}
	}
}

func TestAFailedSaveIsRetried(t *testing.T) {
	l, c := newCounted(t)
	blocker := filepath.Join(t.TempDir(), "file")
	if err := os.WriteFile(blocker, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	good := c.Path
	c.Path = filepath.Join(blocker, "counters.json")
	ingest(l, countsLine(5, 1, boDelta), epoch)
	if err := c.Save(); err == nil {
		t.Fatal("saved under a file")
	}
	c.Path = good
	if err := c.Save(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(good); err != nil {
		t.Error("the totals were forgotten after the failed save")
	}
}

func TestAnUnreadableFileIsSetAsideAndCountingStartsAgain(t *testing.T) {
	path := filepath.Join(t.TempDir(), "counters.json")
	if err := os.WriteFile(path, []byte(`{"version":1,"players":`), 0o600); err != nil {
		t.Fatal(err)
	}
	c := &Counters{Path: path}
	if err := c.Load(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(path + ".unreadable"); err != nil {
		t.Errorf("the unreadable file was not kept: %v", err)
	}
	l := New(time.Minute, 100, nil)
	l.Counters = c
	ingest(l, countsLine(5, 1, boDelta), epoch)
	if bo, ok := c.Lookup("-22"); !ok || bo.Broken != 1 {
		t.Errorf("totals = %+v, %v", bo, ok)
	}
}

func TestTheTotalsAreBounded(t *testing.T) {
	l, c := newCounted(t)
	var kinds, items []string
	for i := 0; i < maxMobTypes+20; i++ {
		kinds = append(kinds, fmt.Sprintf(`"mob%d":1`, i))
	}
	// Records may name only so many types, so send the mob types in slices.
	for from, seq := 0, 1; from < len(kinds); from, seq = from+maxDeltaTypes, seq+1 {
		to := min(from+maxDeltaTypes, len(kinds))
		ingest(l, countsLine(5, seq, `{"i":"-11","k":{`+strings.Join(kinds[from:to], ",")+`}}`), epoch)
	}
	alex, _ := c.Lookup("-11")
	if len(alex.Kills) != maxMobTypes+1 || alex.Kills[otherType] != 20 {
		t.Errorf("%d mob types, other = %d", len(alex.Kills), alex.Kills[otherType])
	}

	for i := 0; i < maxPlayers+10; i++ {
		items = append(items, fmt.Sprintf(`{"i":"%d","j":1}`, 1000+i))
	}
	for from, seq := 0, 100; from < len(items); from, seq = from+maxDeltas, seq+1 {
		ingest(l, countsLine(5, seq, strings.Join(items[from:min(from+maxDeltas, len(items))], ",")), epoch)
	}
	c.mu.Lock()
	n := len(c.players)
	c.mu.Unlock()
	if n != maxPlayers {
		t.Errorf("%d players kept, want %d", n, maxPlayers)
	}
}

func TestOnlyTheLatestRunsOfThePackAreRemembered(t *testing.T) {
	l, c := newCounted(t)
	for boot := 1; boot <= maxSources+2; boot++ {
		ingest(l, countsLine(boot, 1, boDelta), epoch)
	}
	c.mu.Lock()
	n := len(c.sources)
	c.mu.Unlock()
	if n != maxSources {
		t.Errorf("%d runs remembered, want %d", n, maxSources)
	}
}

func TestAMapWithoutCountersIgnoresCountsRecords(t *testing.T) {
	l := New(time.Minute, 100, nil)
	ingest(l, countsLine(5, 1, boDelta), epoch)
	ingest(l, playersLine, epoch)
	if got := l.Store.Snapshot("overworld", epoch).Players; len(got) != 1 {
		t.Errorf("players = %d after a counts record", len(got))
	}
}

// The whole path from the bridge: a poller that answers with the same
// records twice, as a bridge does when a poll failed after it replied.
type repeatingPoller struct {
	mu      sync.Mutex
	batches []Batch
}

func (p *repeatingPoller) Poll(ctx context.Context, _ int64, _ time.Duration) (Batch, error) {
	p.mu.Lock()
	if len(p.batches) > 0 {
		b := p.batches[0]
		p.batches = p.batches[1:]
		p.mu.Unlock()
		return b, nil
	}
	p.mu.Unlock()
	<-ctx.Done()
	return Batch{}, ctx.Err()
}

func TestTheSourceNeverCountsADeltaTwiceWhenTheBridgeRepeatsItself(t *testing.T) {
	l, c := newCounted(t)
	ring := []RawRecord{
		{ID: 1, At: epoch, Data: countsLine(5, 1, boDelta)},
		{ID: 2, At: epoch, Data: countsLine(5, 2, boDelta)},
	}
	repeat := append(append([]RawRecord(nil), ring...), RawRecord{ID: 3, At: epoch, Data: countsLine(5, 3, boDelta)})
	// A bridge that restarted answers from 1 again.
	restarted := []RawRecord{{ID: 1, At: epoch, Data: countsLine(5, 3, boDelta)}, {ID: 2, At: epoch, Data: countsLine(5, 4, boDelta)}}
	p := &repeatingPoller{batches: []Batch{{Records: ring}, {Records: repeat}, {Gap: true, Records: restarted}}}
	s := &Source{Poller: p, Layer: l, Wait: time.Second}

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { s.Run(ctx); close(done) }()
	eventually(t, "all four lines counted", func() bool {
		bo, _ := c.Lookup("-22")
		return bo.Broken >= 4
	})
	cancel()
	<-done
	if bo, _ := c.Lookup("-22"); bo.Broken != 4 {
		t.Errorf("broken = %d, want 4", bo.Broken)
	}
}
