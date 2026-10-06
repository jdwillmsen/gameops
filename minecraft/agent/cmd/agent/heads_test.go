package main

import (
	"bytes"
	"context"
	"image/color"
	"image/png"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/sandertv/gophertunnel/minecraft/protocol"
	"github.com/sandertv/gophertunnel/minecraft/protocol/packet"

	"github.com/jdwillmsen/gameops/minecraft/agent/internal/heads"
	"github.com/jdwillmsen/gameops/minecraft/agent/internal/mapclient"
	"github.com/jdwillmsen/gameops/minecraft/agent/pkg/logging"
)

type headReports chan []mapclient.PlayerHead

func (r headReports) ReportHeads(_ context.Context, players []mapclient.PlayerHead) error {
	r <- players
	return nil
}

func (r headReports) next(t *testing.T) []mapclient.PlayerHead {
	t.Helper()
	select {
	case players := <-r:
		return players
	case <-time.After(5 * time.Second):
		t.Fatal("no report of heads")
		return nil
	}
}

// wireSkin is a synthetic skin as the packet carries one: a face of one
// shade of red and nothing else.
func wireSkin(shade byte) protocol.Skin {
	px := make([]byte, 64*64*4)
	for y := 8; y < 16; y++ {
		for x := 8; x < 16; x++ {
			copy(px[(y*64+x)*4:], []byte{shade, 0, 0, 255})
		}
	}
	return protocol.Skin{SkinImageWidth: 64, SkinImageHeight: 64, SkinData: px, SkinResourcePatch: []byte(`{"geometry":{"default":"geometry.humanoid.custom"}}`)}
}

func shadeOf(t *testing.T, head []byte) byte {
	t.Helper()
	img, err := png.Decode(bytes.NewReader(head))
	if err != nil {
		t.Fatalf("head is not a PNG: %v", err)
	}
	r, _, _, _ := img.At(4, 4).RGBA()
	return byte(r >> 8)
}

// The skins the map draws come from two packets, and a departure names a
// player by the UUID alone.
func TestWatchHeads_FollowsThePlayerListAndSkinChanges(t *testing.T) {
	reports := make(headReports, 16)
	w := heads.New(reports, logging.New("error"), time.Hour)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go w.Run(ctx)
	w.Begin()

	steve, alex := uuid.New(), uuid.New()
	watchHeads(&packet.PlayerList{Entries: []protocol.PlayerListEntry{
		{ActionType: protocol.PlayerListActionAdd, UUID: steve, XUID: "2535400000000001", Username: "Steve", Skin: wireSkin(200)},
		{ActionType: protocol.PlayerListActionAdd, UUID: alex, XUID: "2535400000000002", Username: "Alex", Skin: wireSkin(100)},
	}}, w)
	got := reports.next(t)
	if len(got) != 2 || got[0].Gamertag != "Steve" || shadeOf(t, got[0].Head) != 200 || got[1].Gamertag != "Alex" || shadeOf(t, got[1].Head) != 100 {
		t.Fatalf("after the list, reported %d players", len(got))
	}

	watchHeads(&packet.PlayerSkin{UUID: alex, Skin: wireSkin(50)}, w)
	if got := reports.next(t); len(got) != 2 || shadeOf(t, got[1].Head) != 50 {
		t.Error("a change of skin did not change the head")
	}

	watchHeads(&packet.PlayerList{Entries: []protocol.PlayerListEntry{{ActionType: protocol.PlayerListActionRemove, UUID: steve}}}, w)
	if got := reports.next(t); len(got) != 1 || got[0].Gamertag != "Alex" {
		t.Errorf("after Steve left, reported %d players", len(got))
	}

	// Any other packet is none of its business, and with no map there is
	// no watch at all.
	watchHeads(&packet.Text{Message: "hello"}, w)
	watchHeads(&packet.PlayerSkin{UUID: alex, Skin: wireSkin(1)}, nil)
}

// What a head, or the badge in place of one, is made from is carried over
// from the packet; the pixels of the extra images and the identifiers of
// the parts are not.
func TestHeadSkin_CarriesTheModelTheColoursAndTheShapeOfTheRest(t *testing.T) {
	wire := wireSkin(200)
	wire.PersonaSkin, wire.PremiumSkin, wire.ProfileHash, wire.SkinID = true, true, "abc", "skin-id"
	wire.SkinGeometry = []byte(`{"minecraft:geometry":[]}`)
	wire.SkinColour = color.RGBA{224, 172, 105, 255}
	wire.Animations = []protocol.SkinAnimation{{ImageWidth: 32, ImageHeight: 64, ImageData: make([]byte, 32*64*4), AnimationType: 1, FrameCount: 2, ExpressionType: 1}}
	wire.PersonaPieces = []protocol.PersonaPiece{{PieceID: "piece", PieceType: protocol.PieceTypeHair}, {PieceType: protocol.PieceTypeEyes}}
	wire.PieceTintColours = []protocol.PersonaPieceTintColour{
		{PieceType: "persona_eyes", Colours: [4]color.RGBA{{1, 2, 3, 255}}},
		{PieceType: "persona_hair", Colours: [4]color.RGBA{{40, 20, 90, 255}, {9, 9, 9, 255}}},
	}

	got := headSkin(wire)
	if !got.Persona || string(got.Geometry) != string(wire.SkinGeometry) || got.Width != 64 || len(got.Data) != 64*64*4 {
		t.Errorf("skin %dx%d, persona %v, geometry %q", got.Width, got.Height, got.Persona, got.Geometry)
	}
	if got.Tints.Skin != wire.SkinColour || got.Tints.Hair != (color.RGBA{40, 20, 90, 255}) {
		t.Errorf("tints %+v", got.Tints)
	}
	if len(got.Animations) != 1 || got.Animations[0] != (heads.Animation{Type: 1, Expression: 1, Width: 32, Height: 64, Frames: 2, Bytes: 32 * 64 * 4}) {
		t.Errorf("animations %+v", got.Animations)
	}
	made := got.Made
	if made.ID != "skin-id" || !made.Premium || !made.Hashed || len(made.Pieces) != 2 || made.Pieces[0] != protocol.PieceTypeHair || len(made.Tinted) != 2 || made.Tinted[1] != "persona_hair" {
		t.Errorf("made %+v", made)
	}
	if plain := headSkin(wireSkin(1)); plain.Persona || plain.Made.Hashed || plain.Tints.Hair != (color.RGBA{}) || len(plain.Animations) != 0 {
		t.Errorf("a plain skin: %+v", plain.Made)
	}
}
