package camera

import (
	"bytes"
	"encoding/binary"
	"image"
	"image/draw"
	"image/png"
	"testing"

	"github.com/google/uuid"
	"github.com/sandertv/gophertunnel/minecraft/protocol"
	"github.com/sandertv/gophertunnel/minecraft/protocol/packet"
)

// pixels returns a w x h RGBA buffer whose red channel is the x position and
// green the y, so a copied region can be recognised.
func pixels(w, h int) []byte {
	b := make([]byte, w*h*4)
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			i := (y*w + x) * 4
			b[i], b[i+1], b[i+3] = byte(x), byte(y), 255
		}
	}
	return b
}

func decodePNG(t *testing.T, b []byte) *image.NRGBA {
	t.Helper()
	img, err := png.Decode(bytes.NewReader(b))
	if err != nil {
		t.Fatal(err)
	}
	out := image.NewNRGBA(img.Bounds())
	draw.Draw(out, out.Bounds(), img, img.Bounds().Min, draw.Src)
	return out
}

func TestSkinOfKeepsAFullSkinAndRefusesOddSizes(t *testing.T) {
	good := protocol.Skin{SkinImageWidth: 64, SkinImageHeight: 64, SkinData: pixels(64, 64), SkinResourcePatch: []byte(`{"geometry":{"default":"geometry.humanoid.customSlim"}}`)}
	s, ok := skinOf(good)
	if !ok || !s.slim {
		t.Fatalf("ok %v slim %v", ok, s.slim)
	}
	if img := decodePNG(t, s.png); img.Bounds().Dx() != 64 || img.Pix[0] != 0 || img.Pix[4] != 1 {
		t.Errorf("pixels changed: %v", img.Bounds())
	}
	if hd, ok := skinOf(protocol.Skin{SkinImageWidth: 128, SkinImageHeight: 128, SkinData: pixels(128, 128)}); !ok || hd.slim {
		t.Errorf("128x128: ok %v slim %v", ok, hd.slim)
	}
	for name, bad := range map[string]protocol.Skin{
		"empty":     {},
		"short":     {SkinImageWidth: 64, SkinImageHeight: 64, SkinData: make([]byte, 10)},
		"not 64s":   {SkinImageWidth: 100, SkinImageHeight: 100, SkinData: pixels(100, 100)},
		"wide":      {SkinImageWidth: 64, SkinImageHeight: 16, SkinData: pixels(64, 16)},
		"too large": {SkinImageWidth: 1024, SkinImageHeight: 1024, SkinData: pixels(1024, 1024)},
	} {
		if _, ok := skinOf(bad); ok {
			t.Errorf("%s skin was accepted", name)
		}
	}
}

func TestLegacySkinGetsItsLeftLimbsMirroredFromTheRightOnes(t *testing.T) {
	s, ok := skinOf(protocol.Skin{SkinImageWidth: 64, SkinImageHeight: 32, SkinData: pixels(64, 32)})
	if !ok {
		t.Fatal("a 64x32 skin was refused")
	}
	img := decodePNG(t, s.png)
	if img.Bounds().Dy() != 64 {
		t.Fatalf("height %d", img.Bounds().Dy())
	}
	// The left arm's front face starts at x=36 on row 52 and is the right
	// arm's (44, 20) turned left to right.
	at := func(x, y int) (byte, byte) { p := img.Pix[img.PixOffset(x, y):]; return p[0], p[1] }
	if r, g := at(36, 52); r != 47 || g != 20 {
		t.Errorf("left arm front corner = %d,%d, want 47,20", r, g)
	}
	if r, g := at(39, 63); r != 44 || g != 31 {
		t.Errorf("left arm front far corner = %d,%d, want 44,31", r, g)
	}
}

// readFrames collects every frame of one type from a viewer's output.
func skinFrames(ms [][]byte) map[uint64]skinInfo {
	out := map[uint64]skinInfo{}
	for _, f := range ofType(ms, frameSkin) {
		id, k := binary.Uvarint(f[1:])
		p := 1 + k
		flags := f[p]
		n, k := binary.Uvarint(f[p+1:])
		p += 1 + k
		out[id] = skinInfo{png: f[p : p+int(n)], slim: flags&skinSlim != 0}
	}
	return out
}

func TestListedSkinsReachViewersOnThePlayerThatOwnsThem(t *testing.T) {
	h, _ := newTestHub(t, 2)
	live := attach(t, h)
	mustDrain(t, live)
	id := uuid.New()
	other := uuid.New()
	h.Apply(&packet.PlayerList{Entries: []protocol.PlayerListEntry{
		{ActionType: protocol.PlayerListActionAdd, UUID: id, Skin: protocol.Skin{SkinImageWidth: 64, SkinImageHeight: 64, SkinData: pixels(64, 64)}},
		{ActionType: protocol.PlayerListActionAdd, UUID: other, Skin: protocol.Skin{SkinImageWidth: 64, SkinImageHeight: 64, SkinData: pixels(64, 64)}},
	}}, &sink{})
	h.Apply(&packet.AddPlayer{EntityRuntimeID: 8, Username: "Alex", UUID: id}, &sink{})
	got := skinFrames(mustDrain(t, live))
	if len(got) != 1 || got[8].png == nil {
		t.Fatalf("live skins = %v", got)
	}
	// A skin change reaches the player already in view.
	h.Apply(&packet.PlayerSkin{UUID: id, Skin: protocol.Skin{SkinImageWidth: 64, SkinImageHeight: 64, SkinData: pixels(64, 64), SkinResourcePatch: []byte("customSlim")}}, &sink{})
	if got := skinFrames(mustDrain(t, live)); !got[8].slim {
		t.Errorf("changed skin = %v", got)
	}
	// A later viewer is told in its head.
	if late := skinFrames(mustDrain(t, attach(t, h))); !late[8].slim || len(late) != 1 {
		t.Errorf("late skins = %v", late)
	}
}

func TestPlayerWithNoListedSkinGetsNoFrame(t *testing.T) {
	h, _ := newTestHub(t, 1)
	h.Apply(&packet.AddPlayer{EntityRuntimeID: 8, Username: "Alex", UUID: uuid.New()}, &sink{})
	if got := skinFrames(mustDrain(t, attach(t, h))); len(got) != 0 {
		t.Errorf("skins = %v", got)
	}
}

func TestOwnSkinIsSentAsIdZero(t *testing.T) {
	h, _ := newTestHub(t, 1)
	h.SetOwnSkin(64, 64, pixels(64, 64), false)
	if got := skinFrames(mustDrain(t, attach(t, h))); len(got) != 1 || got[0].png == nil {
		t.Errorf("skins = %v", got)
	}
}

func TestDimensionChangeKeepsSkins(t *testing.T) {
	h, _ := newTestHub(t, 1)
	live := attach(t, h)
	h.SetOwnSkin(64, 64, pixels(64, 64), false)
	id := uuid.New()
	h.Apply(&packet.PlayerList{Entries: []protocol.PlayerListEntry{
		{ActionType: protocol.PlayerListActionAdd, UUID: id, Skin: protocol.Skin{SkinImageWidth: 64, SkinImageHeight: 64, SkinData: pixels(64, 64)}},
	}}, &sink{})
	mustDrain(t, live)
	h.Apply(&packet.ChangeDimension{Dimension: 1}, &sink{})
	// The page forgets every skin on the reset frame, the unit's own too.
	if got := skinFrames(mustDrain(t, live)); got[0].png == nil {
		t.Errorf("own skin after the reset = %v", got)
	}
	h.Apply(&packet.AddPlayer{EntityRuntimeID: 8, Username: "Alex", UUID: id}, &sink{})
	if got := skinFrames(mustDrain(t, live)); got[8].png == nil {
		t.Errorf("skin of a player met again = %v", got)
	}
}

func TestPlayerLeavingFreesTheirSkin(t *testing.T) {
	h, _ := newTestHub(t, 1)
	id := uuid.New()
	h.Apply(&packet.PlayerList{Entries: []protocol.PlayerListEntry{
		{ActionType: protocol.PlayerListActionAdd, UUID: id, Skin: protocol.Skin{SkinImageWidth: 64, SkinImageHeight: 64, SkinData: pixels(64, 64)}},
	}}, &sink{})
	h.Apply(&packet.PlayerList{Entries: []protocol.PlayerListEntry{{ActionType: protocol.PlayerListActionRemove, UUID: id}}}, &sink{})
	h.Apply(&packet.AddPlayer{EntityRuntimeID: 8, Username: "Alex", UUID: id}, &sink{})
	if got := skinFrames(mustDrain(t, attach(t, h))); len(got) != 0 {
		t.Errorf("skins = %v", got)
	}
}

func armourFrames(ms [][]byte) map[uint64]armourSlots {
	out := map[uint64]armourSlots{}
	for _, f := range ofType(ms, frameArmour) {
		n, k := binary.Uvarint(f[1:])
		p := 1 + k
		for ; n > 0; n-- {
			id, k := binary.Uvarint(f[p:])
			p += k
			var worn armourSlots
			for i := range worn {
				l := int(f[p])
				worn[i] = string(f[p+1 : p+1+l])
				p += 1 + l
			}
			out[id] = worn
		}
	}
	return out
}

func item(id int32) protocol.ItemInstance {
	return protocol.ItemInstance{StackNetworkID: 1, Stack: protocol.ItemStack{ItemType: protocol.ItemType{NetworkID: id}, Count: 1}}
}

func TestWornArmourIsSentForOtherEntitiesAndTheUnit(t *testing.T) {
	h, _ := newTestHub(t, 2)
	h.SetItems([]protocol.ItemEntry{{Name: "minecraft:iron_helmet", RuntimeID: 1}, {Name: "minecraft:iron_boots", RuntimeID: 2}})
	live := attach(t, h)
	mustDrain(t, live)
	h.Apply(&packet.AddPlayer{EntityRuntimeID: 8, Username: "Basalt"}, &sink{})
	h.Apply(&packet.MobArmourEquipment{EntityRuntimeID: 8, Helmet: item(1), Boots: item(2)}, &sink{})
	// The unit's own armour comes from its armour window.
	h.Apply(&packet.InventoryContent{WindowID: protocol.WindowIDArmour, Content: []protocol.ItemInstance{item(1), {}, {}, item(2)}}, &sink{})
	got := armourFrames(mustDrain(t, live))
	want := armourSlots{"iron_helmet", "", "", "iron_boots"}
	if got[8] != want || got[0] != want {
		t.Fatalf("armour = %v", got)
	}
	// Taking the helmet off updates one slot.
	h.Apply(&packet.InventorySlot{WindowID: protocol.WindowIDArmour, Slot: 0, NewItem: protocol.ItemInstance{}}, &sink{})
	if got := armourFrames(mustDrain(t, live)); got[0] != (armourSlots{"", "", "", "iron_boots"}) {
		t.Errorf("after removal = %v", got)
	}
	// A late viewer learns what is worn from its head.
	late := armourFrames(mustDrain(t, attach(t, h)))
	if late[8] != want || late[0] != (armourSlots{"", "", "", "iron_boots"}) {
		t.Errorf("late armour = %v", late)
	}
}

func TestArmourOfAnEntityNotInViewIsIgnored(t *testing.T) {
	h, _ := newTestHub(t, 1)
	h.Apply(&packet.MobArmourEquipment{EntityRuntimeID: 99, Helmet: item(1)}, &sink{})
	if got := armourFrames(mustDrain(t, attach(t, h))); len(got) != 0 {
		t.Errorf("armour = %v", got)
	}
}

func TestSkinReasonsAndSlimDetection(t *testing.T) {
	opaque := func(w, h int) []byte { return pixels(w, h) }
	patch := func(g string) []byte { return []byte(`{"geometry":{"default":"` + g + `"}}`) }
	for name, c := range map[string]struct {
		s      protocol.Skin
		reason string
		slim   bool
	}{
		"wide":             {protocol.Skin{SkinImageWidth: 64, SkinImageHeight: 64, SkinData: opaque(64, 64), SkinResourcePatch: patch("geometry.humanoid.custom"), ArmSize: protocol.ArmSizeWide}, skinAccepted, false},
		"slim by patch":    {protocol.Skin{SkinImageWidth: 64, SkinImageHeight: 64, SkinData: opaque(64, 64), SkinResourcePatch: patch("geometry.humanoid.customSlim"), ArmSize: protocol.ArmSizeWide}, skinAccepted, true},
		"named wide wins":  {protocol.Skin{SkinImageWidth: 64, SkinImageHeight: 64, SkinData: opaque(64, 64), SkinResourcePatch: patch("geometry.humanoid.custom"), ArmSize: protocol.ArmSizeSlim}, skinAccepted, false},
		"slim by arm size": {protocol.Skin{SkinImageWidth: 128, SkinImageHeight: 128, SkinData: opaque(128, 128), SkinResourcePatch: []byte(`{}`), ArmSize: protocol.ArmSizeSlim}, skinAccepted, true},
		"custom geometry":  {protocol.Skin{SkinImageWidth: 64, SkinImageHeight: 64, SkinData: opaque(64, 64), SkinResourcePatch: patch("geometry.cow")}, skinAcceptedCustom, false},
		"persona":          {protocol.Skin{SkinImageWidth: 64, SkinImageHeight: 64, SkinData: opaque(64, 64), PersonaSkin: true}, skinAcceptedPersona, false},
		"persona blank":    {protocol.Skin{SkinImageWidth: 64, SkinImageHeight: 64, SkinData: make([]byte, 64*64*4), PersonaSkin: true}, skinTransparent, false},
		"no data":          {protocol.Skin{PersonaSkin: true}, skinNoData, false},
		"odd size":         {protocol.Skin{SkinImageWidth: 100, SkinImageHeight: 100, SkinData: opaque(100, 100)}, skinBadSize, false},
		"short":            {protocol.Skin{SkinImageWidth: 64, SkinImageHeight: 64, SkinData: make([]byte, 10)}, skinBadLength, false},
	} {
		info, reason := decodeSkin(c.s)
		if reason != c.reason || (info.png != nil && info.slim != c.slim) {
			t.Errorf("%s: reason %q slim %v, want %q %v", name, reason, info.slim, c.reason, c.slim)
		}
	}
}

func TestSkinsAtTheCapStillShowOnPlayersInView(t *testing.T) {
	h, _ := newTestHub(t, 1)
	live := attach(t, h)
	mustDrain(t, live)
	sk := protocol.Skin{SkinImageWidth: 64, SkinImageHeight: 64, SkinData: pixels(64, 64)}
	for i := 0; i < maxSkins; i++ {
		h.setSkin(uuid.New(), sk)
	}
	// A newcomer in view evicts a skin of someone out of view.
	id := uuid.New()
	h.Apply(&packet.AddPlayer{EntityRuntimeID: 8, Username: "Alex", UUID: id}, &sink{})
	h.setSkin(id, sk)
	if got := skinFrames(mustDrain(t, live)); got[8].png == nil {
		t.Fatalf("skins = %v", got)
	}
	if len(h.skins) != maxSkins || h.skins[id] == nil {
		t.Errorf("remembered %d, has newcomer %v", len(h.skins), h.skins[id] != nil)
	}
	// With everyone remembered in view, a further player is shown unremembered.
	h.mu.Lock()
	for i := range h.skins {
		h.ents[uint64(1000+len(h.ents))] = &entity{ID: uint64(1000 + len(h.ents)), UUID: i}
	}
	h.mu.Unlock()
	late := uuid.New()
	h.Apply(&packet.AddPlayer{EntityRuntimeID: 9, Username: "Bo", UUID: late}, &sink{})
	h.setSkin(late, sk)
	if got := skinFrames(mustDrain(t, live)); got[9].png == nil {
		t.Errorf("player past the cap got no skin: %v", got)
	}
}
