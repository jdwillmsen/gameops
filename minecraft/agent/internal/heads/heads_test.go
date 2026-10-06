package heads

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"image/color"
	"image/png"
	"math"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jdwillmsen/gameops/minecraft/agent/internal/mapclient"
	"github.com/jdwillmsen/gameops/minecraft/agent/pkg/logging"
	"github.com/jdwillmsen/gameops/minecraft/agent/pkg/skin"
)

const (
	steveUUID, steveXUID = "00000000-0000-0000-0000-000000000001", "2535400000000001"
	alexUUID, alexXUID   = "00000000-0000-0000-0000-000000000002", "2535400000000002"
)

type fakeMap struct {
	mu      sync.Mutex
	reports [][]mapclient.PlayerHead
	err     error
	got     chan struct{}
}

func (f *fakeMap) ReportHeads(_ context.Context, players []mapclient.PlayerHead) error {
	f.mu.Lock()
	f.reports = append(f.reports, players)
	err := f.err
	f.mu.Unlock()
	f.got <- struct{}{}
	return err
}

func (f *fakeMap) count() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.reports)
}

// next waits for one more report and returns it.
func (f *fakeMap) next(t *testing.T) map[string]mapclient.PlayerHead {
	t.Helper()
	select {
	case <-f.got:
	case <-time.After(5 * time.Second):
		t.Fatal("no report reached the map")
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	out := map[string]mapclient.PlayerHead{}
	for _, p := range f.reports[len(f.reports)-1] {
		out[p.XUID] = p
	}
	return out
}

func (f *fakeMap) quiet(t *testing.T, d time.Duration) {
	t.Helper()
	select {
	case <-f.got:
		t.Fatal("a report reached the map when none was due")
	case <-time.After(d):
	}
}

func start(t *testing.T, interval time.Duration) (*Watch, *fakeMap) {
	t.Helper()
	m := &fakeMap{got: make(chan struct{}, 64)}
	w := New(m, logging.New("error"), interval)
	w.settle = 5 * time.Millisecond
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { w.Run(ctx); close(done) }()
	t.Cleanup(func() { cancel(); <-done })
	return w, m
}

// classic is a synthetic 64x64 skin whose face is one colour, with no hat.
func classic(face color.NRGBA) Skin {
	px := make([]byte, 64*64*4)
	for y := 8; y < 16; y++ {
		for x := 8; x < 16; x++ {
			copy(px[(y*64+x)*4:], []byte{face.R, face.G, face.B, 255})
		}
	}
	return Skin{Width: 64, Height: 64, Data: px, ResourcePatch: []byte(`{"geometry":{"default":"geometry.humanoid.custom"}}`)}
}

func faceOf(t *testing.T, head []byte) color.NRGBA {
	t.Helper()
	img, err := png.Decode(bytes.NewReader(head))
	if err != nil {
		t.Fatalf("the head is not a PNG: %v", err)
	}
	if b := img.Bounds(); b.Dx() != 8 || b.Dy() != 8 {
		t.Fatalf("the head is %dx%d, want 8x8", b.Dx(), b.Dy())
	}
	return color.NRGBAModel.Convert(img.At(4, 4)).(color.NRGBA)
}

var (
	red  = color.NRGBA{200, 0, 0, 255}
	blue = color.NRGBA{0, 0, 200, 255}
)

func TestEveryoneOnTheListIsReportedWithTheirHead(t *testing.T) {
	w, m := start(t, time.Hour)
	w.Begin()
	w.List([]Entry{
		{UUID: steveUUID, XUID: steveXUID, Name: "Steve", Skin: classic(red)},
		{UUID: alexUUID, XUID: alexXUID, Name: "Alex", Skin: classic(blue)},
	})
	got := m.next(t)
	if len(got) != 2 || got[steveXUID].Gamertag != "Steve" || got[alexXUID].Gamertag != "Alex" {
		t.Fatalf("reported %v", got)
	}
	if c := faceOf(t, got[steveXUID].Head); c != red {
		t.Errorf("Steve's head is %v", c)
	}
	if c := faceOf(t, got[alexXUID].Head); c != blue {
		t.Errorf("Alex's head is %v", c)
	}
}

// avatarOf checks that a head is the generated badge for that player and
// nothing of the skin they sent.
func avatarOf(t *testing.T, xuid string, tints skin.Tints, head []byte) {
	t.Helper()
	img, err := png.Decode(bytes.NewReader(head))
	if err != nil {
		t.Fatalf("the head is not a PNG: %v", err)
	}
	want, _ := skin.Avatar(xuid, tints)
	if img.Bounds() != want.Bounds() {
		t.Fatalf("the head is %v, want the avatar's %v", img.Bounds(), want.Bounds())
	}
	for y := range want.Bounds().Dy() {
		for x := range want.Bounds().Dx() {
			if got := color.NRGBAModel.Convert(img.At(x, y)).(color.NRGBA); got != want.NRGBAAt(x, y) {
				t.Fatalf("pixel %d,%d is %v, want the avatar's %v", x, y, got, want.NRGBAAt(x, y))
			}
		}
	}
	if len(head) > 8<<10 {
		t.Errorf("the avatar is %d bytes; the map takes 8 KiB", len(head))
	}
}

// A skin is whatever another player's client sent. One this package cannot
// safely take a face from costs nobody else anything, and that player is
// drawn as the badge made from who they are, never as a bare marker and
// never as pixels of the skin that was refused.
func TestASkinThatCannotBeTrustedIsDrawnAsAnAvatar(t *testing.T) {
	persona := classic(red)
	persona.Persona = true
	otherModel := classic(red)
	otherModel.ResourcePatch = []byte(`{"geometry":{"default":"geometry.some_pack.dragon"}}`)
	brokenPatch := classic(red)
	brokenPatch.ResourcePatch = []byte(`{"geometry":`)
	brokenPatchWithModel := classic(red)
	brokenPatchWithModel.ResourcePatch = []byte(`{"geometry":`)
	brokenPatchWithModel.Geometry = model("", 64, 64, headCube(0, 0))
	hugePatch := classic(red)
	hugePatch.ResourcePatch = append([]byte(`{"geometry":{"default":"geometry.humanoid.custom"},"pad":"`), append(bytes.Repeat([]byte("a"), maxPatch), `"}`...)...)
	short := classic(red)
	short.Data = short.Data[:len(short.Data)-4]
	oddSize := Skin{Width: 100, Height: 100, Data: make([]byte, 100*100*4)}
	enormous := Skin{Width: 1 << 31, Height: 1 << 31, Data: make([]byte, 16)}
	empty := Skin{}
	// What production saw of one character-creator skin: no image to speak of.
	onePixel := Skin{Width: 1, Height: 1, Data: []byte{0, 0, 0, 0}, Persona: true, ResourcePatch: []byte(`{"geometry":{"default":"geometry.persona_0123456789abcdef_0_1"}}`)}
	noHeadCube := modelled(red, 64, 64, `{"name":"head"}`)
	brokenModel := modelled(red, 64, 64, headCube(0, 0))
	brokenModel.Geometry = brokenModel.Geometry[:len(brokenModel.Geometry)-3]
	offImage := modelled(red, 64, 64, headCube(60, 60))
	hugeModel := modelled(red, 64, 64, headCube(0, 0))
	hugeModel.Geometry = append(hugeModel.Geometry, bytes.Repeat([]byte(" "), skin.MaxGeometryBytes)...)
	enormousModelled := modelled(red, 64, 64, headCube(0, 0))
	enormousModelled.Width, enormousModelled.Height = 1<<31, 1<<31
	shortModelled := modelled(red, 64, 64, headCube(0, 0))
	shortModelled.Data = shortModelled.Data[:len(shortModelled.Data)-4]

	for name, c := range map[string]struct {
		bad Skin
		why string
	}{
		"a persona skin with no model":   {persona, skipPersona},
		"a persona skin of one pixel":    {onePixel, skipPersona},
		"a skin for another model":       {otherModel, skipGeometry},
		"an unreadable model name":       {brokenPatch, skipGeometry},
		"an unreadable name and a model": {brokenPatchWithModel, skipGeometry},
		"an over-long model name":        {hugePatch, skipGeometry},
		"fewer bytes than its size":      {short, skipImage},
		"a size no skin has":             {oddSize, skipImage},
		"a size that overflows":          {enormous, skipImage},
		"no image at all":                {empty, skipImage},
		"a model with no head cube":      {noHeadCube, string(skin.ErrHeadEmpty)},
		"a model that is not JSON":       {brokenModel, string(skin.ErrGeometryJSON)},
		"a model too large to read":      {hugeModel, string(skin.ErrGeometrySize)},
		"a head off the image":           {offImage, string(skin.ErrHeadUV)},
		"a modelled size that overflows": {enormousModelled, skipImage},
		"a modelled skin short of bytes": {shortModelled, string(skin.ErrHeadTexture)},
	} {
		t.Run(name, func(t *testing.T) {
			w, m := start(t, time.Hour)
			w.Begin()
			w.List([]Entry{
				{UUID: steveUUID, XUID: steveXUID, Name: "Steve", Skin: c.bad},
				{UUID: alexUUID, XUID: alexXUID, Name: "Alex", Skin: classic(blue)},
			})
			got := m.next(t)
			if p, named := got[steveXUID]; !named || p.Gamertag != "Steve" {
				t.Fatalf("the player is not named: %v", got)
			}
			avatarOf(t, steveXUID, skin.Tints{}, got[steveXUID].Head)
			if c := faceOf(t, got[alexXUID].Head); c != blue {
				t.Errorf("the player beside them lost their head: %v", c)
			}

			fields := logging.Fields{}
			if img, kind := w.face(steveXUID, c.bad, fields); img != nil || kind != "" || fields["skipped"] != c.why {
				t.Errorf("face %v of kind %q, skipped for %v, want %q", img != nil, kind, fields["skipped"], c.why)
			}
			if name := fmt.Sprint(fields["geometry"]); strings.Contains(name, "0123456789abcdef") {
				t.Errorf("the model is logged as %q, with the skin's identifier", name)
			}
		})
	}
}

// model is a synthetic geometry file of one model with the bones given.
func model(name string, tw, th int, bones ...string) []byte {
	return []byte(fmt.Sprintf(`{"format_version":"1.14.0","minecraft:geometry":[{"description":{"identifier":%q,"texture_width":%d,"texture_height":%d},"bones":[%s]}]}`,
		name, tw, th, strings.Join(bones, ",")))
}

func headCube(u, v int) string {
	return fmt.Sprintf(`{"name":"head","cubes":[{"origin":[-4,24,-4],"size":[8,8,8],"uv":[%d,%d]}]}`, u, v)
}

const packModel = "geometry.some_pack.fox"

// modelled is a synthetic skin of one colour all over, for a model of its
// own. Where the face is, only the model says.
func modelled(all color.NRGBA, w, h int, bones ...string) Skin {
	px := make([]byte, w*h*4)
	for i := 0; i < len(px); i += 4 {
		copy(px[i:], []byte{all.R, all.G, all.B, 255})
	}
	return Skin{Width: uint32(w), Height: uint32(h), Data: px, Geometry: model(packModel, 64, h*64/w, bones...),
		ResourcePatch: []byte(`{"geometry":{"default":"` + packModel + `"}}`)}
}

func paint(s Skin, x, y, side int, c color.NRGBA) {
	for dy := range side {
		for dx := range side {
			copy(s.Data[((y+dy)*int(s.Width)+x+dx)*4:], []byte{c.R, c.G, c.B, c.A})
		}
	}
}

// A skin for a model of its own says where its head is, and is given the
// head found there: here one kept low on a tall image, where no classic
// skin has anything, with the hat laid over half of it.
func TestASkinForItsOwnModelGetsTheHeadTheModelPointsTo(t *testing.T) {
	s := modelled(blue, 64, 128, headCube(0, 64), `{"name":"hat","cubes":[{"size":[8,8,8],"inflate":0.5,"uv":[32,64]}]}`)
	paint(s, 8, 72, 8, red)
	paint(s, 40, 72, 8, color.NRGBA{})
	persona := modelled(blue, 128, 128, headCube(16, 16))
	persona.Persona = true
	paint(persona, 48, 48, 16, red)

	w, m := start(t, time.Hour)
	w.Begin()
	w.List([]Entry{
		{UUID: steveUUID, XUID: steveXUID, Name: "Steve", Skin: s},
		{UUID: alexUUID, XUID: alexXUID, Name: "Alex", Skin: persona},
	})
	got := m.next(t)
	if c := faceOf(t, got[steveXUID].Head); c != red {
		t.Errorf("the head is %v, want the face the model points to", c)
	}
	img, err := png.Decode(bytes.NewReader(got[alexXUID].Head))
	if err != nil || img.Bounds().Dx() != 16 || color.NRGBAModel.Convert(img.At(8, 8)).(color.NRGBA) != red {
		t.Errorf("a persona skin that brings a model of cubes: %v, err %v", img.Bounds(), err)
	}

	fields := logging.Fields{}
	if _, kind := w.face(steveXUID, s, fields); kind != headGeometry || fields["skipped"] != nil || fields["face"] != "(8,72)-(16,80)" || fields["hat"] != "(40,72)-(48,80)" || fields["geometry"] != packModel {
		t.Errorf("kind %q, fields %v", kind, fields)
	}
}

// The classic path is the one every skin took before, and takes still: a
// standard skin is never read through a model, even one it brings itself
// that points somewhere else.
func TestAClassicSkinIsCroppedAsBeforeWhateverModelItBrings(t *testing.T) {
	s := classic(red)
	s.Geometry = model("geometry.humanoid.custom", 64, 64, headCube(32, 32))
	w, _ := start(t, time.Hour)
	fields := logging.Fields{}
	img, kind := w.face(steveXUID, s, fields)
	if kind != headReal || img == nil || img.NRGBAAt(4, 4) != red || fields["skipped"] != nil {
		t.Errorf("kind %q, fields %v", kind, fields)
	}
}

// The colours a character-creator skin gives are the player's own, and the
// badge wears them. Any other skin's are not asked.
func TestAnAvatarWearsAPersonaSkinsColours(t *testing.T) {
	tints := skin.Tints{Skin: color.RGBA{224, 172, 105, 255}, Hair: color.RGBA{40, 20, 90, 255}}
	persona := Skin{Width: 1, Height: 1, Data: []byte{0, 0, 0, 0}, Persona: true, Tints: tints}
	notPersona := Skin{Width: 100, Height: 100, Data: make([]byte, 100*100*4), Tints: tints}
	w, m := start(t, time.Hour)
	w.Begin()
	w.List([]Entry{
		{UUID: steveUUID, XUID: steveXUID, Name: "Steve", Skin: persona},
		{UUID: alexUUID, XUID: alexXUID, Name: "Alex", Skin: notPersona},
	})
	got := m.next(t)
	avatarOf(t, steveXUID, tints, got[steveXUID].Head)
	avatarOf(t, alexXUID, skin.Tints{}, got[alexXUID].Head)
}

// What is logged of a skin is cut to a length and to kinds, and can always
// be written, whatever the skin claimed.
func TestWhatIsLoggedOfASkinIsBoundedAndCarriesNoIdentifier(t *testing.T) {
	s := modelled(red, 64, 64, headCube(0, 0))
	s.Persona = true
	s.ResourcePatch = []byte(`{"geometry":{"default":"geometry.persona_0123456789abcdef_0_1","animated_face":"geometry.animated_face_persona-0123456789abcdef","cape":7,
		"a":"","b":"","c":"","d":"","e":"","f":"","g":"","h":""}}`)
	for i := range 3 * maxListed {
		s.Animations = append(s.Animations, Animation{Type: uint32(i), Width: 32, Height: 64, Frames: float32(math.NaN()), Bytes: 8192})
	}
	s.Animations[1].Frames = float32(math.Inf(1))
	s.Animations[2].Frames = 2
	for i := range 100 {
		s.Made.Pieces = append(s.Made.Pieces, uint32(i))
		s.Made.Tinted = append(s.Made.Tinted, "persona_0123456789abcdef")
	}
	s.Made.ID = "0123456789abcdef-persona"
	g, err := skin.ParseGeometry(s.Geometry)
	if err != nil {
		t.Fatal(err)
	}
	fields := described(steveXUID, s, g)
	line, err := json.Marshal(fields)
	if err != nil {
		t.Fatalf("the line cannot be written: %v", err)
	}
	if strings.Contains(string(line), "0123456789abcdef") {
		t.Errorf("the line carries the skin's identifier: %s", line)
	}
	if len(line) > 8<<10 {
		t.Errorf("the line is %d bytes", len(line))
	}
	slots := fields["slots"].(map[string]string)
	if len(slots) != maxListed || slots["animated_face"] != "geometry.animated_face_persona-#" || slots["cape"] != "?" {
		t.Errorf("slots %v", slots)
	}
	if fields["animation_count"] != 3*maxListed || fields["piece_count"] != 100 || len(fields["pieces"].([]uint32)) != 4*maxListed || len(fields["tinted_pieces"].([]string)) != maxListed {
		t.Errorf("fields %v", fields)
	}
	if !strings.Contains(string(line), `"frames":-1`) || !strings.Contains(string(line), `"frames":2`) {
		t.Errorf("frame counts: %s", line)
	}
	if strings.Count(string(line), `"frames"`) != maxListed {
		t.Errorf("%d animations listed", strings.Count(string(line), `"frames"`))
	}

	// A patch too long to be one names nothing.
	s.ResourcePatch = append([]byte(`{"geometry":{"default":"x"},"pad":"`), append(bytes.Repeat([]byte("a"), maxPatch), `"}`...)...)
	if fields := described(steveXUID, s, nil); fields["slots"] != nil || fields["model"] != nil {
		t.Errorf("fields %v", fields)
	}
}

func TestASkinNamingNoModelIsAStandardOne(t *testing.T) {
	w, m := start(t, time.Hour)
	w.Begin()
	bare := classic(red)
	bare.ResourcePatch = nil
	slim := classic(blue)
	slim.ResourcePatch = []byte(`{"geometry":{"default":"geometry.humanoid.customSlim"}}`)
	w.List([]Entry{{UUID: steveUUID, XUID: steveXUID, Name: "Steve", Skin: bare}, {UUID: alexUUID, XUID: alexXUID, Name: "Alex", Skin: slim}})
	got := m.next(t)
	if faceOf(t, got[steveXUID].Head) != red || faceOf(t, got[alexXUID].Head) != blue {
		t.Error("a standard skin gave no head")
	}
}

func TestLeavingAndChangingSkinAreReported(t *testing.T) {
	w, m := start(t, time.Hour)
	w.Begin()
	w.List([]Entry{
		{UUID: steveUUID, XUID: steveXUID, Name: "Steve", Skin: classic(red)},
		{UUID: alexUUID, XUID: alexXUID, Name: "Alex", Skin: classic(blue)},
	})
	m.next(t)

	w.Reskin(steveUUID, classic(blue))
	if got := m.next(t); faceOf(t, got[steveXUID].Head) != blue {
		t.Error("a changed skin did not change the head")
	}
	// Named by UUID alone, as a departure is.
	w.List([]Entry{{UUID: alexUUID, Remove: true}})
	if got := m.next(t); len(got) != 1 || got[steveXUID].Gamertag != "Steve" {
		t.Errorf("after Alex left, reported %v", got)
	}
	// A skin for nobody on the list is nobody's.
	w.Reskin(alexUUID, classic(red))
	m.quiet(t, 50*time.Millisecond)
}

func TestAnEntryThatIsNotASignedInPlayerIsLeftOut(t *testing.T) {
	w, m := start(t, time.Hour)
	w.Begin()
	w.List([]Entry{
		{UUID: steveUUID, XUID: "", Name: "Offline", Skin: classic(red)},
		{UUID: alexUUID, XUID: "not-a-number", Name: "Odd", Skin: classic(red)},
		{UUID: "00000000-0000-0000-0000-000000000003", XUID: alexXUID, Name: "", Skin: classic(red)},
	})
	if got := m.next(t); len(got) != 0 {
		t.Errorf("reported %v", got)
	}
}

// Two replicas run, and only the one in the game sees skins. The other
// holds an empty list, and reporting it would wipe what the first sent.
func TestNothingIsReportedOutsideASession(t *testing.T) {
	w, m := start(t, 10*time.Millisecond)
	w.List([]Entry{{UUID: steveUUID, XUID: steveXUID, Name: "Steve", Skin: classic(red)}})
	w.Reskin(steveUUID, classic(blue))
	m.quiet(t, 100*time.Millisecond)
	if n := m.count(); n != 0 {
		t.Errorf("%d reports from a process that is in no game", n)
	}
}

func TestTheEndOfASessionIsReportedOnceAndThenNothing(t *testing.T) {
	w, m := start(t, 20*time.Millisecond)
	w.Begin()
	w.List([]Entry{{UUID: steveUUID, XUID: steveXUID, Name: "Steve", Skin: classic(red)}})
	if got := m.next(t); len(got) != 1 {
		t.Fatalf("reported %v", got)
	}
	w.End()
	// Reports of the session itself may still be in flight; the last word
	// has to be that nobody is online.
	deadline := time.After(5 * time.Second)
	for {
		if got := m.next(t); len(got) == 0 {
			break
		}
		select {
		case <-deadline:
			t.Fatal("the map was never told the session ended")
		default:
		}
	}
	m.quiet(t, 150*time.Millisecond)

	// The next session starts from nobody, not from who was here before.
	w.Begin()
	w.List([]Entry{{UUID: alexUUID, XUID: alexXUID, Name: "Alex", Skin: classic(blue)}})
	got := m.next(t)
	// The interval can come round between the session beginning and its
	// first list, and report that nobody is here yet.
	if len(got) == 0 {
		got = m.next(t)
	}
	if len(got) != 1 || got[alexXUID].Gamertag != "Alex" {
		t.Errorf("the next session reported %v", got)
	}
}

// The map keeps heads in memory. Repeating the list is what restores them
// after it restarts, and what gets past a report that failed.
func TestAnUnchangedListIsSentAgainAndAFailureIsNotTheEnd(t *testing.T) {
	w, m := start(t, 20*time.Millisecond)
	m.err = errors.New("map: report heads: connection refused")
	w.Begin()
	w.List([]Entry{{UUID: steveUUID, XUID: steveXUID, Name: "Steve", Skin: classic(red)}})
	for range 3 {
		if got := m.next(t); len(got) != 1 {
			t.Fatalf("reported %v", got)
		}
	}
}

func TestANilWatchIsAnAgentWithNoMap(t *testing.T) {
	var w *Watch
	w.Begin()
	w.List([]Entry{{UUID: steveUUID, XUID: steveXUID, Name: "Steve", Skin: classic(red)}})
	w.Reskin(steveUUID, classic(red))
	w.End()
	w.Run(context.Background())
}
