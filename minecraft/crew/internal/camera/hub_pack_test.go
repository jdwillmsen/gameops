package camera

import (
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"

	"github.com/sandertv/gophertunnel/minecraft/protocol"
	"github.com/sandertv/gophertunnel/minecraft/protocol/packet"

	"github.com/jdwillmsen/gameops/minecraft/crew/internal/atlas"
)

// wildlifePack is the resource pack the lab ships; its entities are the
// fixture for everything a pack's own animals need.
const wildlifePack = "../../../../packs/lab-mods-wildlife/resource_pack"

func TestVariantAndSittingReachViewersWithoutDisturbingTheDye(t *testing.T) {
	h, _ := newTestHub(t, 3)
	live := attach(t, h)
	drain(t, live)
	meta := protocol.EntityMetadata{protocol.EntityDataKeyVariant: int32(1)}
	h.Apply(&packet.AddActor{EntityRuntimeID: 9, EntityType: "gameops:tiger", EntityMetadata: meta}, &sink{})
	if got := parseLooks(t, mustDrain(t, live)); got[9] != (entityLook{Color: 1 << 4}) {
		t.Fatalf("spawn look = %+v", got[9])
	}

	h.Apply(&packet.SetActorData{EntityRuntimeID: 9, EntityMetadata: flagsMeta(protocol.EntityDataFlagSitting)}, &sink{})
	if got := parseLooks(t, mustDrain(t, live)); got[9] != (entityLook{Flags: entSitting, Color: 1 << 4}) {
		t.Fatalf("sitting look = %+v", got[9])
	}

	// A variant-only update keeps the flag, and standing clears only it.
	h.Apply(&packet.SetActorData{EntityRuntimeID: 9, EntityMetadata: protocol.EntityMetadata{protocol.EntityDataKeyVariant: int32(0)}}, &sink{})
	if got := parseLooks(t, mustDrain(t, live)); got[9] != (entityLook{Flags: entSitting}) {
		t.Fatalf("after variant 0 = %+v", got[9])
	}
	h.Apply(&packet.SetActorData{EntityRuntimeID: 9, EntityMetadata: flagsMeta(protocol.EntityDataFlagTamed)}, &sink{})
	if got := parseLooks(t, mustDrain(t, live)); got[9] != (entityLook{}) {
		t.Fatalf("after standing = %+v", got[9])
	}
}

func TestAnAttackSwingIsForwardedOnceAndNotKept(t *testing.T) {
	h, _ := newTestHub(t, 3)
	live := attach(t, h)
	drain(t, live)
	h.Apply(&packet.AddActor{EntityRuntimeID: 9, EntityType: "gameops:tiger"}, &sink{})
	drain(t, live)
	h.Apply(&packet.ActorEvent{EntityRuntimeID: 9, EventType: packet.ActorEventStartAttacking}, &sink{})
	h.Apply(&packet.ActorEvent{EntityRuntimeID: 9, EventType: packet.ActorEventHurt}, &sink{})
	h.Apply(&packet.ActorEvent{EntityRuntimeID: 404, EventType: packet.ActorEventStartAttacking}, &sink{})
	got := ofType(mustDrain(t, live), frameActorEvent)
	if len(got) != 1 || string(got[0]) != string([]byte{frameActorEvent, 9, eventAttack}) {
		t.Fatalf("event frames = % x", got)
	}
	if late := ofType(mustDrain(t, attach(t, h)), frameActorEvent); len(late) != 0 {
		t.Errorf("a late viewer was sent the old swing: % x", late)
	}
}

func TestPackEntitiesAreServedAndAnnouncedWithoutAnAtlas(t *testing.T) {
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
	if status("/v1/camera/pack.json") != 404 || status("/v1/camera/pack.png") != 404 {
		t.Fatal("pack models served before they were built")
	}
	if status("/v1/camera/molang.js") != 200 {
		t.Fatal("molang.js is not served")
	}
	live := attach(t, h)
	drain(t, live)

	png, table, _, err := atlas.BuildPackEntities(wildlifePack)
	if err != nil {
		t.Fatal(err)
	}
	if err := h.SetPackEntities(png, table); err != nil {
		t.Fatal(err)
	}
	if got := mustDrain(t, live); len(ofType(got, frameModels)) != 1 {
		t.Fatalf("live viewer frames = %v, want one models frame", got)
	}
	if status("/v1/camera/pack.json") != 200 || status("/v1/camera/pack.png") != 200 {
		t.Fatal("pack models not served after the build")
	}
	if late := mustDrain(t, attach(t, h)); len(ofType(late, frameModels)) != 1 {
		t.Errorf("late viewer head has no models frame: %v", late)
	}
}

func TestPageHandlesTheActorEventFrame(t *testing.T) {
	if want := "case " + strconv.Itoa(int(frameActorEvent)) + ":"; !strings.Contains(string(page), want) {
		t.Errorf("page does not contain %q", want)
	}
}
