package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

const labCompose = "services:\n  lab:\n    environment:\n      ONLINE_MODE: \"false\"\n"

func goodConfig() config {
	return config{lab: "lab", addr: "127.0.0.1:19397", compose: writeCompose(nil, labCompose), units: "PlcA,PlcB", maxDuration: time.Minute, maxPlacements: 10, floorY: 100}
}

func writeCompose(_ *testing.T, body string) string {
	dir, err := os.MkdirTemp("", "placecal")
	if err != nil {
		panic(err)
	}
	p := filepath.Join(dir, "compose.yaml")
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		panic(err)
	}
	return p
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
		"colosseum origin":   func(c *config) { c.originX, c.originZ = -300, -758 },
		"huge origin":        func(c *config) { c.originX = 1 << 30 },
		"path version":       func(c *config) { c.serverVersion = "../1.2" },
		"missing compose":    func(c *config) { c.compose = "/nonexistent/compose.yaml" },
		"online compose": func(c *config) {
			c.compose = writeCompose(nil, "services:\n  lab:\n    environment:\n      ONLINE_MODE: \"true\"\n")
		},
		"other service name": func(c *config) {
			c.compose = writeCompose(nil, "services:\n  mc:\n    environment:\n      ONLINE_MODE: \"false\"\n")
		},
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
	c.compose = writeCompose(nil, strings.Replace(labCompose, "lab:", "minecraft:", 1))
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
