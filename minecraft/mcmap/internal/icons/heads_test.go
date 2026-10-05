package icons

import (
	"bytes"
	"fmt"
	"image"
	"image/jpeg"
	"testing"
	"time"
)

const (
	steveXUID = "2535400000000001"
	alexXUID  = "2535400000000002"
)

func TestHeadsAreServedByGamertagAndReencoded(t *testing.T) {
	const smuggled = "<script>alert(1)</script>"
	sent := withText(t, picture(t, 8, 8, red), smuggled)
	h := &Heads{}
	refused, err := h.Replace([]Report{{XUID: steveXUID, Gamertag: "Steve", Head: sent}, {XUID: alexXUID, Gamertag: "Alex"}})
	if err != nil || refused != 0 {
		t.Fatalf("refused %d, err %v", refused, err)
	}
	head, ok := h.ByGamertag("sTEVE")
	if !ok {
		t.Fatal("no head for a gamertag in another case")
	}
	if bytes.Contains(head.PNG, []byte(smuggled)) || bytes.Equal(head.PNG, sent) {
		t.Error("the agent's bytes were kept as they came, text chunk and all")
	}
	if got := colourOf(t, head.PNG); got != red {
		t.Errorf("the head changed in re-encoding: %v", got)
	}
	if _, ok := h.ByGamertag("Alex"); ok {
		t.Error("a player reported without a head has one")
	}
	versions, self := h.Listing(alexXUID)
	if len(versions) != 1 || versions["steve"] != head.Version || self != "alex" {
		t.Errorf("listing is %v for %q", versions, self)
	}
}

func TestAMalformedOrOversizedHeadIsRefusedAndCostsOnlyItself(t *testing.T) {
	var jpg bytes.Buffer
	_ = jpeg.Encode(&jpg, image.NewNRGBA(image.Rect(0, 0, 8, 8)), nil)
	bomb := declaring(picture(t, 8, 8, red), 60000, 60000)
	noise := make([]byte, 0, maxHeadBytes+1)
	for i := range maxHeadBytes + 1 {
		noise = append(noise, byte(i*31))
	}
	for name, bad := range map[string][]byte{
		"not an image":                           []byte("definitely not a PNG"),
		"a JPEG":                                 jpg.Bytes(),
		"too large a side":                       picture(t, maxHeadSide+1, maxHeadSide+1, red),
		"too small a side":                       picture(t, minHeadSide-1, minHeadSide-1, red),
		"not square":                             picture(t, 8, 16, red),
		"a whole skin":                           picture(t, 64, 64, red),
		"a declared size the data does not back": bomb,
		"over the byte limit":                    append(picture(t, 8, 8, red), noise...),
		"a truncated PNG":                        picture(t, 8, 8, red)[:40],
	} {
		t.Run(name, func(t *testing.T) {
			h := &Heads{}
			refused, err := h.Replace([]Report{
				{XUID: steveXUID, Gamertag: "Steve", Head: bad},
				{XUID: alexXUID, Gamertag: "Alex", Head: picture(t, 8, 8, blue)},
			})
			if err != nil {
				t.Fatalf("one bad head refused the whole report: %v", err)
			}
			if refused != 1 {
				t.Errorf("refused %d heads, want 1", refused)
			}
			if _, ok := h.ByGamertag("Steve"); ok {
				t.Errorf("a head that is %s is being served", name)
			}
			if _, ok := h.ByGamertag("Alex"); !ok {
				t.Error("the good head beside it was lost")
			}
		})
	}
}

func TestAReportThatIsNotAListOfPlayersIsRefusedWhole(t *testing.T) {
	good := Report{XUID: alexXUID, Gamertag: "Alex", Head: picture(t, 8, 8, blue)}
	var crowd []Report
	for i := range MaxPlayers + 1 {
		crowd = append(crowd, Report{XUID: fmt.Sprintf("25354%011d", i), Gamertag: fmt.Sprintf("P%d", i)})
	}
	for name, reports := range map[string][]Report{
		"an XUID that is not a number": {good, {XUID: "console", Gamertag: "Server"}},
		"no gamertag":                  {good, {XUID: steveXUID}},
		"a gamertag with a newline":    {good, {XUID: steveXUID, Gamertag: "Ste\nve"}},
		"an over-long gamertag":        {good, {XUID: steveXUID, Gamertag: string(bytes.Repeat([]byte("a"), maxGamertag+1))}},
		"invalid UTF-8":                {good, {XUID: steveXUID, Gamertag: "Ste\xffve"}},
		"one player twice":             {good, good},
		"more players than a server":   crowd,
	} {
		t.Run(name, func(t *testing.T) {
			h := &Heads{}
			if _, err := h.Replace([]Report{{XUID: steveXUID, Gamertag: "Steve", Head: picture(t, 8, 8, red)}}); err != nil {
				t.Fatal(err)
			}
			if _, err := h.Replace(reports); err == nil {
				t.Fatalf("a report with %s was accepted", name)
			}
			if _, ok := h.ByGamertag("Steve"); !ok {
				t.Error("a refused report still replaced the one before it")
			}
			if _, ok := h.ByGamertag("Alex"); ok {
				t.Error("part of a refused report was kept")
			}
		})
	}
}

// The live layer names a player by gamertag alone. If two online players
// answer to one, either head could be the wrong one, so neither is served.
func TestTwoPlayersUnderOneGamertagGetNoHead(t *testing.T) {
	h := &Heads{}
	if _, err := h.Replace([]Report{
		{XUID: steveXUID, Gamertag: "Twin", Head: picture(t, 8, 8, red)},
		{XUID: alexXUID, Gamertag: "TWIN", Head: picture(t, 8, 8, blue)},
		{XUID: "2535400000000003", Gamertag: "Other", Head: picture(t, 8, 8, green)},
	}); err != nil {
		t.Fatal(err)
	}
	if head, ok := h.ByGamertag("twin"); ok {
		t.Errorf("a gamertag two players hold was answered with the head coloured %v", colourOf(t, head.PNG))
	}
	versions, self := h.Listing(steveXUID)
	if _, listed := versions["twin"]; listed || len(versions) != 1 || self != "" {
		t.Errorf("listing is %v for %q; the shared gamertag must be in neither", versions, self)
	}
}

func TestAGamertagChangeTakesTheHeadWithIt(t *testing.T) {
	h := &Heads{}
	steves := picture(t, 8, 8, red)
	if _, err := h.Replace([]Report{{XUID: steveXUID, Gamertag: "Steve", Head: steves}}); err != nil {
		t.Fatal(err)
	}
	// Steve is renamed and somebody else arrives under his old name.
	if _, err := h.Replace([]Report{
		{XUID: steveXUID, Gamertag: "Stefan", Head: steves},
		{XUID: alexXUID, Gamertag: "Steve", Head: picture(t, 8, 8, blue)},
	}); err != nil {
		t.Fatal(err)
	}
	if head, ok := h.ByGamertag("Steve"); !ok || colourOf(t, head.PNG) != blue {
		t.Error("the old name still answers with the head of the player who gave it up")
	}
	if head, ok := h.ByGamertag("Stefan"); !ok || colourOf(t, head.PNG) != red {
		t.Error("the head did not follow its player to the new name")
	}
	if _, self := h.Listing(steveXUID); self != "stefan" {
		t.Errorf("the renamed player is told they are %q", self)
	}
}

func TestAPlayerWhoLeftIsForgotten(t *testing.T) {
	h := &Heads{}
	if _, err := h.Replace([]Report{{XUID: steveXUID, Gamertag: "Steve", Head: picture(t, 8, 8, red)}}); err != nil {
		t.Fatal(err)
	}
	if _, err := h.Replace(nil); err != nil {
		t.Fatal(err)
	}
	if _, ok := h.ByGamertag("Steve"); ok {
		t.Error("a player in no report still has a head")
	}
}

func TestHeadsNobodyIsKeepingAreNotServed(t *testing.T) {
	now := time.Date(2026, 10, 5, 12, 0, 0, 0, time.UTC)
	h := &Heads{TTL: time.Minute, Now: func() time.Time { return now }}
	if _, err := h.Replace([]Report{{XUID: steveXUID, Gamertag: "Steve", Head: picture(t, 8, 8, red)}}); err != nil {
		t.Fatal(err)
	}
	now = now.Add(59 * time.Second)
	if _, ok := h.ByGamertag("Steve"); !ok {
		t.Fatal("a fresh report is not served")
	}
	now = now.Add(2 * time.Second)
	if _, ok := h.ByGamertag("Steve"); ok {
		t.Error("a report older than its life is still served")
	}
	if versions, self := h.Listing(steveXUID); len(versions) != 0 || self != "" {
		t.Errorf("a report older than its life is still listed: %v, %q", versions, self)
	}
}
