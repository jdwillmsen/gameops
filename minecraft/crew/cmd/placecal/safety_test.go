package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/sandertv/gophertunnel/minecraft/protocol"

	"github.com/jdwillmsen/gameops/minecraft/crew/internal/place"
)

type fakeCon struct {
	mu    sync.Mutex
	cmds  []string
	reply func(cmd string) (string, error)
}

func (f *fakeCon) Exec(cmd string) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.cmds = append(f.cmds, cmd)
	if f.reply != nil {
		return f.reply(cmd)
	}
	return "", nil
}

func (f *fakeCon) has(sub string) bool {
	for _, c := range f.cmds {
		if strings.Contains(c, sub) {
			return true
		}
	}
	return false
}

func TestCheckOrigin(t *testing.T) {
	cases := []struct {
		x, z, y, n int
		ok         bool
	}{
		{1000, 1000, 220, 4, true},
		{-300, -758, 220, 1, false},
		{-190, -700, 220, 1, false},
		{-181 + 7, -700, 220, 1, true}, // west edge of the bay clears x -181
		{-460, -758, 220, 3, false},    // third bay walks into it
		{-300, -630, 220, 1, true},     // south of the footprint, marker included
		{-300, -640, 220, 1, false},    // marker cells reach z-13
		{0, 0, 2, 1, false},
		{1 << 30, 0, 100, 1, false},
	}
	for _, c := range cases {
		if err := checkOrigin(c.x, c.z, c.y, c.n); (err == nil) != c.ok {
			t.Errorf("%+v: %v", c, err)
		}
	}
}

func TestCheckVersion(t *testing.T) {
	for _, v := range []string{"1.26.51", "1.21", "1.2.3.4.5"} {
		if checkVersion(v) != nil {
			t.Errorf("%q refused", v)
		}
	}
	for _, v := range []string{"", "1", "../1.2", "1.2/3", "1.2.3.4.5.6", "v1.2", "1.2\n"} {
		if checkVersion(v) == nil {
			t.Errorf("%q accepted", v)
		}
	}
}

func TestCheckCompose(t *testing.T) {
	good := writeCompose(nil, labCompose)
	if err := checkCompose(good, "lab"); err != nil {
		t.Fatal(err)
	}
	if checkCompose(good, "other") == nil {
		t.Error("wrong service accepted")
	}
	if checkCompose(writeCompose(nil, "services:\n  lab:\n    environment:\n      ONLINE_MODE: true\n"), "lab") == nil {
		t.Error("online accepted")
	}
}

func TestGamerulesRoundTrip(t *testing.T) {
	vals := map[string]string{"domobspawning": "true", "dodaylightcycle": "false", "dotiledrops": "true", "doweathercycle": "false"}
	f := &fakeCon{reply: func(cmd string) (string, error) {
		if n, ok := strings.CutPrefix(cmd, "gamerule "); ok && !strings.Contains(n, " ") {
			return n + " = " + vals[n], nil
		}
		return "", nil
	}}
	con := &safeConsole{c: f}
	saved, err := readGamerules(con)
	if err != nil || saved["dodaylightcycle"] != "false" || saved["domobspawning"] != "true" {
		t.Fatalf("%v %v", saved, err)
	}
	f.cmds = nil
	if err := restoreGamerules(con, saved); err != nil {
		t.Fatal(err)
	}
	if !f.has("gamerule domobspawning true") || !f.has("gamerule dodaylightcycle false") {
		t.Fatalf("%v", f.cmds)
	}
	f.reply = func(string) (string, error) { return "Unknown rule", nil }
	if _, err := readGamerules(con); err == nil {
		t.Error("unreadable rule accepted")
	}
}

func TestAddArea(t *testing.T) {
	for reply, ok := range map[string]bool{
		"Added ticking area pcal0": true, "": true,
		"Cannot add ticking area: limit exceeded": false, "A ticking area with that name already exists": false,
	} {
		con := &safeConsole{c: &fakeCon{reply: func(string) (string, error) { return reply, nil }}}
		if err := addArea(con, bay{ox: 1, f: 2, oz: 3}, "pcal0"); (err == nil) != ok {
			t.Errorf("%q: %v", reply, err)
		}
	}
}

func TestRemoveStaleAreas(t *testing.T) {
	f := &fakeCon{}
	removeStaleAreas(&safeConsole{c: f})
	if len(f.cmds) != maxUnits || f.cmds[7] != "tickingarea remove pcal7" {
		t.Fatalf("%v", f.cmds)
	}
}

func TestCheckBayEmpty(t *testing.T) {
	b := bay{ox: 1000, f: 220, oz: 1000}
	empty := &safeConsole{c: &fakeCon{reply: func(string) (string, error) { return "Successfully found the block", nil }}}
	if err := checkBayEmpty(context.Background(), empty, b); err != nil {
		t.Fatal(err)
	}
	busy := &safeConsole{c: &fakeCon{reply: func(string) (string, error) { return "The block at 1,2,3 is Stone (expected: Air)", nil }}}
	if err := checkBayEmpty(context.Background(), busy, b); err == nil || !strings.Contains(err.Error(), "not empty") {
		t.Fatalf("%v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	unloaded := &safeConsole{c: &fakeCon{reply: func(string) (string, error) { return "chunk not loaded", nil }}}
	if err := checkBayEmpty(ctx, unloaded, b); err == nil {
		t.Fatal("unloaded chunk passed")
	}
}

func TestClearBayCoversFloorAndVolume(t *testing.T) {
	f := &fakeCon{reply: func(string) (string, error) { return "12 blocks filled", nil }}
	b := bay{ox: 1000, f: 220, oz: 1000}
	if err := clearBay(context.Background(), &safeConsole{c: f}, b); err != nil {
		t.Fatal(err)
	}
	want := fmt.Sprintf("fill 994 220 996 1006 228 1006 air")
	if len(f.cmds) != 1 || f.cmds[0] != want {
		t.Fatalf("%v want %s", f.cmds, want)
	}
}

// worldFake plays a unit: it reads whatever the console set, unless detached.
type worldFake struct {
	f      *fakeCon
	blocks map[protocol.BlockPos]string
	other  bool
}

func (w *worldFake) read(p protocol.BlockPos) outcome {
	if w.other {
		return outcome{}
	}
	if n, ok := w.blocks[p]; ok {
		return outcome{Placed: true, Name: "minecraft:" + n}
	}
	return outcome{}
}

func (w *worldFake) waitFor(ctx context.Context, _ time.Duration, cond func() bool) bool {
	return cond()
}

func TestCheckMarker(t *testing.T) {
	b := bay{ox: 1000, f: 220, oz: 1000}
	for _, other := range []bool{false, true} {
		w := &worldFake{blocks: map[protocol.BlockPos]string{}, other: other}
		f := &fakeCon{}
		f.reply = func(cmd string) (string, error) {
			var x, y, z int32
			var name string
			if _, err := fmt.Sscanf(cmd, "setblock %d %d %d %s", &x, &y, &z, &name); err == nil {
				w.blocks[protocol.BlockPos{x, y, z}] = name
			}
			return "Block placed", nil
		}
		err := checkMarker(context.Background(), &safeConsole{c: f}, w, b)
		if (err == nil) == other {
			t.Errorf("other=%v: %v", other, err)
		}
		if err := clearMarker(context.Background(), &safeConsole{c: f}, b); err != nil || !f.has("air") {
			t.Fatal(err)
		}
	}
}

func tableOf(n int) *place.Table {
	t := &place.Table{Header: place.Header{Format: place.FormatVersion, ServerVersion: "1.2"}, Blocks: map[string]*place.Block{}}
	for i := 0; i < n; i++ {
		t.Blocks[fmt.Sprintf("b%d", i)] = &place.Block{}
	}
	return t
}

func TestWriteTableAtomic(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "out", "1.2.json.gz")
	if err := writeTableAtomic(path, tableOf(3), false); err != nil {
		t.Fatal(err)
	}
	err := writeTableAtomic(path, tableOf(2), false)
	if !errors.Is(err, errShrinks) {
		t.Fatalf("shrink allowed: %v", err)
	}
	if err := writeTableAtomic(path, tableOf(2), true); err != nil {
		t.Fatal(err)
	}
	if err := writeTableAtomic(path, tableOf(5), false); err != nil {
		t.Fatal(err)
	}
	ents, _ := os.ReadDir(filepath.Dir(path))
	if len(ents) != 1 {
		t.Fatalf("temp files left: %v", ents)
	}
}
