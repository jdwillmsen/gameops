package live

import (
	"encoding/json"
	"fmt"
	"sync"
	"testing"
	"time"
)

func msg(s string) Message { return Message{Data: []byte(s), Ready: time.Now()} }

func recv(t *testing.T, sub *Subscription) string {
	t.Helper()
	select {
	case m := <-sub.C:
		return string(m.Data)
	case <-time.After(2 * time.Second):
		t.Fatal("no frame")
		return ""
	}
}

func idle(t *testing.T, sub *Subscription) {
	t.Helper()
	select {
	case m := <-sub.C:
		t.Fatalf("unexpected frame %s", m.Data)
	default:
	}
}

func TestHubFansOutPerDimension(t *testing.T) {
	h := &Hub{}
	a, _ := h.Subscribe("overworld")
	b, _ := h.Subscribe("overworld")
	n, _ := h.Subscribe("nether")

	h.Publish("overworld", msg("ow"))
	if recv(t, a) != "ow" || recv(t, b) != "ow" {
		t.Fatal("an overworld subscriber did not get the overworld frame")
	}
	idle(t, n)

	b.Close("client")
	b.Close("client")
	h.Publish("overworld", msg("ow2"))
	h.Publish("nether", msg("n"))
	if recv(t, a) != "ow2" || recv(t, n) != "n" {
		t.Fatal("frames after a subscriber left went astray")
	}
	idle(t, b)
}

// One browser on a bad connection stops reading. Everyone else's markers
// must keep moving, and when it reads again it gets the present.
func TestHubDropsForASlowSubscriberWithoutBlocking(t *testing.T) {
	h := &Hub{}
	slow, _ := h.Subscribe("overworld")
	fast, _ := h.Subscribe("overworld")

	done := make(chan struct{})
	go func() {
		defer close(done)
		for i := range 50 {
			h.Publish("overworld", msg(fmt.Sprint(i)))
			if got := recv(t, fast); got != fmt.Sprint(i) {
				t.Errorf("fast subscriber got %s at frame %d", got, i)
			}
		}
	}()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("publishing blocked on the subscriber that was not reading")
	}
	if got := recv(t, slow); got != "49" {
		t.Errorf("the slow subscriber caught up to frame %s, want the newest", got)
	}
	idle(t, slow)
}

func TestHubRefusesPastItsBound(t *testing.T) {
	h := &Hub{}
	subs := make([]*Subscription, 0, MaxSubscribers)
	for range MaxSubscribers {
		sub, ok := h.Subscribe("overworld")
		if !ok {
			t.Fatal("refused before the bound")
		}
		subs = append(subs, sub)
	}
	if _, ok := h.Subscribe("nether"); ok {
		t.Fatal("accepted past the bound")
	}
	subs[0].Close("client")
	if _, ok := h.Subscribe("nether"); !ok {
		t.Error("a closed subscription did not free its place")
	}
}

func raw(id int64, at time.Time, data string) RawRecord { return RawRecord{ID: id, At: at, Data: data} }

func mobsJSON(gen int, dimension string, n int) string {
	items := make([]Entity, n)
	for i := range items {
		items[i] = Entity{ID: fmt.Sprint(i), Type: "zombie", X: float64(i), Z: float64(gen)}
	}
	b, _ := json.Marshal(map[string]any{"gen": gen, "dim": dimension, "kind": "mobs", "part": 0, "parts": 1, "more": 0, "items": items})
	return string(b)
}

type wireFrame struct {
	At         *time.Time `json:"at"`
	ServerNow  time.Time  `json:"serverNow"`
	Players    []Entity   `json:"players"`
	Mobs       []Entity   `json:"mobs"`
	More       int        `json:"more"`
	Stale      bool       `json:"stale"`
	TTLSeconds float64    `json:"ttlSeconds"`
}

func decode(t *testing.T, data string) wireFrame {
	t.Helper()
	var f wireFrame
	if err := json.Unmarshal([]byte(data), &f); err != nil {
		t.Fatalf("%v: %s", err, data)
	}
	if f.Players == nil || f.Mobs == nil {
		t.Fatalf("a frame's lists must be lists even when empty: %s", data)
	}
	return f
}

func TestLayerPublishesAFrameThenAStaleOneAtTheTTL(t *testing.T) {
	l := New(10*time.Second, 1000, nil)
	sub, _ := l.Subscribe("overworld")

	if f := decode(t, string(l.Current("overworld", t0).Data)); !f.Stale || f.At != nil {
		t.Fatalf("before any record: %+v", f)
	}
	l.Ingest(raw(1, t0, mobsJSON(1, "overworld", 3)), t0.Add(20*time.Millisecond))
	l.Flush(t0.Add(20 * time.Millisecond))
	f := decode(t, recv(t, sub))
	if f.Stale || len(f.Mobs) != 3 || f.At == nil || !f.At.Equal(t0) || !f.ServerNow.Equal(t0.Add(20*time.Millisecond)) || f.TTLSeconds != 10 {
		t.Fatalf("frame = %+v", f)
	}
	l.Flush(t0.Add(5 * time.Second))
	idle(t, sub)

	l.Flush(t0.Add(10 * time.Second))
	if f := decode(t, recv(t, sub)); !f.Stale || len(f.Mobs) != 0 {
		t.Errorf("at the TTL: %+v", f)
	}
}

// The bridge restarted with the server, and by the time it is next asked it
// has issued more ids than the cursor it is asked for, so nothing flags the
// break. The generation counting from the start again is what shows it.
func TestLayerPicksUpAfterAServerRestartNothingFlagged(t *testing.T) {
	l := New(10*time.Second, 1000, nil)
	half, _ := json.Marshal(map[string]any{"gen": 9001, "dim": "overworld", "kind": "mobs", "part": 0, "parts": 2, "more": 0, "items": []Entity{{ID: "old", Type: "cow"}}})
	l.Ingest(raw(40, t0, mobsJSON(9000, "overworld", 5)), t0)
	l.Ingest(raw(41, t0, string(half)), t0)

	// Ids carry on upwards as if nothing happened; generations do not.
	rest, _ := json.Marshal(map[string]any{"gen": 2, "dim": "overworld", "kind": "mobs", "part": 1, "parts": 2, "more": 0, "items": []Entity{{ID: "b", Type: "cow"}}})
	first, _ := json.Marshal(map[string]any{"gen": 2, "dim": "overworld", "kind": "mobs", "part": 0, "parts": 2, "more": 0, "items": []Entity{{ID: "a", Type: "cow"}}})
	later := t0.Add(3 * time.Second)
	l.Ingest(raw(42, later, `{"gen":1,"kind":"tick","scan":2,"interval":1000}`), later)
	l.Ingest(raw(43, later, string(first)), later)
	l.Ingest(raw(44, later, string(rest)), later)
	f := l.Store.Snapshot("overworld", later)
	if got := fmt.Sprint(ids(f.Mobs)); got != "[a b]" || !f.At.Equal(later) {
		t.Errorf("after the restart: %s at %v, want the new server's list", got, f.At)
	}
}

// Age is measured on the bridge's clock, so a record replayed long after it
// was written is not drawn as if it were new.
func TestLayerAgesARecordFromWhenTheBridgeReceivedIt(t *testing.T) {
	l := New(10*time.Second, 1000, nil)
	l.Ingest(raw(1, t0, mobsJSON(1, "overworld", 2)), t0.Add(time.Minute))
	if f := l.Store.Snapshot("overworld", t0.Add(time.Minute)); !f.Stale {
		t.Errorf("a minute-old record is drawn: %+v", f)
	}
	// A bridge clock that runs ahead must not keep a record alive longer.
	l = New(10*time.Second, 1000, nil)
	l.Ingest(raw(1, t0.Add(time.Hour), mobsJSON(1, "overworld", 2)), t0)
	if f := l.Store.Snapshot("overworld", t0.Add(10*time.Second)); !f.Stale {
		t.Errorf("a record from the future outlived the TTL: %+v", f)
	}
}

func TestLayerCountsARecordItCannotReadAndCarriesOn(t *testing.T) {
	l := New(10*time.Second, 1000, nil)
	l.Ingest(raw(1, t0, `[x] [Scripting] hello`), t0)
	l.Ingest(raw(2, t0, `{"gen":1,"kind":"blocks"}`), t0)
	l.Ingest(raw(3, t0, "\x00"+mobsJSON(2, "end", 1)), t0)
	l.Ingest(raw(4, t0, `{"gen":2,"kind":"tick","scan":4,"interval":1000}`), t0)
	if f := l.Store.Snapshot("end", t0); len(f.Mobs) != 1 {
		t.Errorf("the record after the unreadable ones: %+v", f)
	}
}

// Records arriving and lists going stale are flushed from two goroutines.
// Whichever reads the store last must also be the one whose frame a browser
// is left holding, or it shows an older picture until the next sample.
func TestLayerNeverLeavesABrowserOnAnOlderFrame(t *testing.T) {
	l := New(time.Hour, 1000, nil)
	sub, _ := l.Subscribe("overworld")
	for i := range 5000 {
		at := t0.Add(time.Duration(i) * time.Millisecond)
		l.Ingest(raw(0, at, mobsJSON(2*i, "overworld", 1)), at)
		var wg sync.WaitGroup
		wg.Go(func() { l.Flush(at) })
		wg.Go(func() {
			l.Ingest(raw(0, at, mobsJSON(2*i+1, "overworld", 2)), at)
			l.Flush(at)
		})
		wg.Wait()
		if f := decode(t, recv(t, sub)); len(f.Mobs) != 2 {
			t.Fatalf("round %d: the browser is left holding the frame of %d mob, with a newer one of 2 already published", i, len(f.Mobs))
		}
	}
}
