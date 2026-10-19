package camera

import (
	"bytes"
	"encoding/binary"
	"image"
	"image/png"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/jdwillmsen/gameops/minecraft/crew/internal/atlas"
	"github.com/sandertv/gophertunnel/minecraft/protocol"
	"github.com/sandertv/gophertunnel/minecraft/protocol/packet"
)

func TestEntityDataFrameLayout(t *testing.T) {
	f := encodeEntityData(map[uint64]entityLook{300: {Flags: entBaby | entSheared, Color: 14}, 2: {Item: "apple"}})
	want := []byte{frameEntityData, 2,
		2, 0, 0, 5, 'a', 'p', 'p', 'l', 'e',
		0xac, 0x02, 3, 14, 0}
	if !bytes.Equal(f, want) {
		t.Errorf("entity data = % x\nwant        % x", f, want)
	}
	if f := modelsFrame(); !bytes.Equal(f, []byte{frameModels, 1}) {
		t.Errorf("models frame = % x", f)
	}
}

func parseLooks(t *testing.T, ms [][]byte) map[uint64]entityLook {
	t.Helper()
	out := map[uint64]entityLook{}
	for _, f := range ofType(ms, frameEntityData) {
		n, k := binary.Uvarint(f[1:])
		p := 1 + k
		for ; n > 0; n-- {
			id, k := binary.Uvarint(f[p:])
			p += k
			l := entityLook{Flags: f[p], Color: f[p+1]}
			n := int(f[p+2])
			l.Item = string(f[p+3 : p+3+n])
			p += 3 + n
			out[id] = l
		}
	}
	return out
}

func flagsMeta(flags ...int) protocol.EntityMetadata {
	m := protocol.NewEntityMetadata()
	for _, f := range flags {
		if f >= 64 {
			m.SetFlag(protocol.EntityDataKeyFlagsTwo, uint8(f-64))
		} else {
			m.SetFlag(protocol.EntityDataKeyFlags, uint8(f))
		}
	}
	return m
}

func TestBabyShearedAndColourReachViewersAndChangesAreSentOnce(t *testing.T) {
	h, _ := newTestHub(t, 3)
	live := attach(t, h)
	drain(t, live)
	meta := flagsMeta(protocol.EntityDataFlagBaby)
	meta[protocol.EntityDataKeyColorIndex] = byte(14)
	h.Apply(&packet.AddActor{EntityRuntimeID: 9, EntityType: "minecraft:sheep", EntityMetadata: meta}, &sink{})
	got := parseLooks(t, mustDrain(t, live))
	if got[9] != (entityLook{Flags: entBaby, Color: 14}) {
		t.Fatalf("spawn look = %+v", got[9])
	}

	h.Apply(&packet.SetActorData{EntityRuntimeID: 9, EntityMetadata: flagsMeta(protocol.EntityDataFlagBaby, protocol.EntityDataFlagSheared)}, &sink{})
	got = parseLooks(t, mustDrain(t, live))
	if got[9] != (entityLook{Flags: entBaby | entSheared, Color: 14}) {
		t.Fatalf("after shearing = %+v", got[9])
	}

	// A repeat of the same data, or data for a stranger, costs nothing.
	h.Apply(&packet.SetActorData{EntityRuntimeID: 9, EntityMetadata: flagsMeta(protocol.EntityDataFlagBaby, protocol.EntityDataFlagSheared)}, &sink{})
	h.Apply(&packet.SetActorData{EntityRuntimeID: 404, EntityMetadata: flagsMeta(protocol.EntityDataFlagBaby)}, &sink{})
	if frames := ofType(mustDrain(t, live), frameEntityData); len(frames) != 0 {
		t.Errorf("%d needless data frames", len(frames))
	}

	// A colour-only update keeps the flags.
	h.Apply(&packet.SetActorData{EntityRuntimeID: 9, EntityMetadata: protocol.EntityMetadata{protocol.EntityDataKeyColorIndex: byte(3)}}, &sink{})
	got = parseLooks(t, mustDrain(t, live))
	if got[9] != (entityLook{Flags: entBaby | entSheared, Color: 3}) {
		t.Fatalf("after recolour = %+v", got[9])
	}

	late := parseLooks(t, mustDrain(t, attach(t, h)))
	if late[9] != (entityLook{Flags: entBaby | entSheared, Color: 3}) {
		t.Errorf("late viewer look = %+v", late[9])
	}
}

func TestPlainEntitiesSendNoEntityData(t *testing.T) {
	h, _ := newTestHub(t, 2)
	v := attach(t, h)
	drain(t, v)
	h.Apply(&packet.AddActor{EntityRuntimeID: 9, EntityType: "minecraft:cow", EntityMetadata: protocol.NewEntityMetadata()}, &sink{})
	h.Apply(&packet.AddActor{EntityRuntimeID: 10, EntityType: "minecraft:cow"}, &sink{})
	if n := len(ofType(mustDrain(t, v), frameEntityData)); n != 0 {
		t.Errorf("%d data frames for plain entities", n)
	}
}

func TestDroppedItemsCarryTheirName(t *testing.T) {
	h, _ := newTestHub(t, 2)
	h.SetItems([]protocol.ItemEntry{{Name: "minecraft:apple", RuntimeID: 257}})
	v := attach(t, h)
	drain(t, v)
	h.Apply(&packet.AddItemActor{EntityRuntimeID: 20, Item: protocol.ItemInstance{Stack: protocol.ItemStack{ItemType: protocol.ItemType{NetworkID: 257}, Count: 1}}}, &sink{})
	h.Apply(&packet.AddItemActor{EntityRuntimeID: 21, Item: protocol.ItemInstance{Stack: protocol.ItemStack{ItemType: protocol.ItemType{NetworkID: 999}, Count: 1}}}, &sink{})
	got := parseLooks(t, mustDrain(t, v))
	if got[20].Item != "apple" {
		t.Errorf("item = %+v", got[20])
	}
	if _, has := got[21]; has {
		t.Errorf("an unknown item sent data: %+v", got[21])
	}
}

func copyTree(t *testing.T, from, to string) {
	t.Helper()
	err := filepath.WalkDir(from, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(from, p)
		if d.IsDir() {
			return os.MkdirAll(filepath.Join(to, rel), 0o755)
		}
		b, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		return os.WriteFile(filepath.Join(to, rel), b, 0o644)
	})
	if err != nil {
		t.Fatal(err)
	}
}

// entityAtlas is the test pack plus one invented entity.
func entityAtlas(t *testing.T) *atlas.Atlas {
	t.Helper()
	root := t.TempDir()
	copyTree(t, "../atlas/testdata/pack", root)
	write := func(rel, body string) {
		p := filepath.Join(root, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("entity/box.entity.json", `{"minecraft:client_entity":{"description":{"identifier":"minecraft:box","textures":{"default":"textures/entity/box"},"geometry":{"default":"geometry.box"}}}}`)
	write("models/entity/box.geo.json", `{"geometry.box":{"texturewidth":16,"textureheight":16,"bones":[{"name":"body","pivot":[0,0,0],"cubes":[{"origin":[-2,0,-2],"size":[4,4,4],"uv":[0,0]}]}]}}`)
	var b bytes.Buffer
	if err := png.Encode(&b, image.NewNRGBA(image.Rect(0, 0, 16, 16))); err != nil {
		t.Fatal(err)
	}
	write("textures/entity/box.png", b.String())
	a, err := atlas.Build(root)
	if err != nil {
		t.Fatal(err)
	}
	if a.EntitiesErr() != nil {
		t.Fatal(a.EntitiesErr())
	}
	return a
}

func TestModelsAreServedAndAnnouncedOnlyWhenBuilt(t *testing.T) {
	h, _ := newTestHub(t, 3)
	srv := httptest.NewServer(Mount(okBase(), h))
	defer srv.Close()
	status := func(p string) int {
		res, err := http.Get(srv.URL + p)
		if err != nil {
			t.Fatal(err)
		}
		res.Body.Close()
		return res.StatusCode
	}
	if status("/v1/camera/entities.json") != 404 || status("/v1/camera/entities.png") != 404 {
		t.Fatal("models served before the build")
	}
	if status("/v1/camera/entities.js") != 200 {
		t.Fatal("entities.js is not served")
	}

	// An atlas whose pack has no entities announces the atlas and no models.
	live := attach(t, h)
	drain(t, live)
	if err := h.SetAtlas(testAtlas(t)); err != nil {
		t.Fatal(err)
	}
	if got := mustDrain(t, live); len(ofType(got, frameModels)) != 0 {
		t.Fatalf("models announced without any: %v", got)
	}

	if err := h.SetAtlas(entityAtlas(t)); err != nil {
		t.Fatal(err)
	}
	if got := mustDrain(t, live); len(ofType(got, frameModels)) != 1 {
		t.Fatalf("live viewer frames = %v, want one models frame", got)
	}
	if status("/v1/camera/entities.json") != 200 || status("/v1/camera/entities.png") != 200 {
		t.Fatal("models not served after the build")
	}
	if late := mustDrain(t, attach(t, h)); len(ofType(late, frameModels)) != 1 {
		t.Errorf("late viewer head has no models frame: %v", late)
	}
}

// The page and its entity module are embedded and not type-checked, so the
// frame numbers they switch on and the paths they fetch are checked here.
func TestPageHandlesTheEntityFramesAndLoadsItsModule(t *testing.T) {
	for _, s := range []string{
		"from '/v1/camera/entities.js'",
		"case " + strconv.Itoa(int(frameEntityData)) + ":",
		"case " + strconv.Itoa(int(frameModels)) + ":",
	} {
		if !strings.Contains(string(page), s) {
			t.Errorf("page does not contain %q", s)
		}
	}
	for _, s := range []string{"/v1/camera/entities.json", "/v1/camera/entities.png", "/v1/camera/items.json"} {
		if !strings.Contains(string(entitiesJS), s) {
			t.Errorf("entities.js does not fetch %q", s)
		}
	}
}
