package main

import (
	"strings"
	"testing"
	"time"
)

func goodConfig() config {
	return config{lab: "lab", addr: "127.0.0.1:19397", compose: "/tmp/x.yaml", units: "PlcA,PlcB", maxDuration: time.Minute, maxPlacements: 10, floorY: 100}
}

func TestValidateAcceptsTheCrewLab(t *testing.T) {
	names, err := validate(goodConfig())
	if err != nil || len(names) != 2 {
		t.Fatalf("names %v err %v", names, err)
	}
}

func TestValidateRefuses(t *testing.T) {
	cases := map[string]func(*config){
		"other service":      func(c *config) { c.lab = "minecraft" },
		"cluster pod":        func(c *config) { c.lab = "k8s:ns/pod"; c.confirmLab = "k8s:ns/pod" },
		"fwb service":        func(c *config) { c.lab = "fwb-server"; c.confirmLab = "fwb-server" },
		"fwb compose":        func(c *config) { c.compose = "/srv/FWB/compose.yaml" },
		"remote address":     func(c *config) { c.addr = "203.0.113.5:19132" },
		"hostname address":   func(c *config) { c.addr = "mc.example.com:19132" },
		"no port":            func(c *config) { c.addr = "127.0.0.1" },
		"selector in a name": func(c *config) { c.units = "@a" },
		"no units":           func(c *config) { c.units = " , " },
		"forever":            func(c *config) { c.maxDuration = 100 * time.Hour },
		"no lab":             func(c *config) { c.lab = "" },
	}
	for name, mutate := range cases {
		c := goodConfig()
		mutate(&c)
		if _, err := validate(c); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
}

func TestValidateAllowsAConfirmedLabService(t *testing.T) {
	c := goodConfig()
	c.lab, c.confirmLab = "minecraft", "minecraft"
	if _, err := validate(c); err != nil {
		t.Fatal(err)
	}
}

func TestSkipItemKeepsBlocksAndDropsEntitiesAndLiquids(t *testing.T) {
	for _, n := range []string{"water_bucket", "zombie_spawn_egg", "oak_boat", "chest_minecart", "mob_spawner", "egg", "flint_and_steel"} {
		if !skipItem(n) {
			t.Errorf("%s should be skipped", n)
		}
	}
	for _, n := range []string{"oak_stairs", "oak_door", "ladder", "sand", "torch", "tnt", "bed", "crossbow_like_block"} {
		if strings.Contains(n, "crossbow") {
			continue
		}
		if skipItem(n) {
			t.Errorf("%s should be calibrated", n)
		}
	}
}

func TestParsePitches(t *testing.T) {
	got, err := parsePitches("0, -60,60,-90,90")
	if err != nil || len(got) != 5 || got[3] != -90 {
		t.Fatalf("got %v %v", got, err)
	}
	for _, bad := range []string{"", "-60,0", "0,95", "0,x"} {
		if _, err := parsePitches(bad); err == nil {
			t.Errorf("%q accepted", bad)
		}
	}
}
