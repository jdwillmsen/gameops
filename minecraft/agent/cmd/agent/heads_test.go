package main

import (
	"bytes"
	"context"
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
