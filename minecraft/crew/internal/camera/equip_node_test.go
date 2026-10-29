package camera

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// The item rules and frame readers are browser code with no Go counterpart,
// so they are checked by running the shipped module under node.
func TestEquipModule(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node is not installed")
	}
	equip, _ := filepath.Abs("web/equip.js")
	cmd := exec.Command(node, "--test", "testdata/equip.test.mjs")
	cmd.Env = append(os.Environ(), "EQUIP_JS="+equip)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("equip tests failed: %v\n%s", err, out)
	}
}

func TestEquipmentFrameLayout(t *testing.T) {
	f := encodeEquipment(map[uint64]handSlots{
		0:   {{"iron_sword", true}, {}},
		300: {{}, {"shield", false}},
	})
	want := []byte{frameEquipment, 2,
		0, 1, 10, 'i', 'r', 'o', 'n', '_', 's', 'w', 'o', 'r', 'd', 0, 0,
		0xac, 0x02, 0, 0, 0, 6, 's', 'h', 'i', 'e', 'l', 'd'}
	if !bytes.Equal(f, want) {
		t.Errorf("equipment = % x\nwant        % x", f, want)
	}
	f = encodeArmourExtra(map[uint64]armourMods{8: {{Glint: true, Pattern: "coast", Material: "quartz"}, {}, {}, {Glint: true}}})
	want = []byte{frameArmourExtra, 1, 8, 3, 5, 'c', 'o', 'a', 's', 't', 6, 'q', 'u', 'a', 'r', 't', 'z', 0, 0, 1}
	if !bytes.Equal(f, want) {
		t.Errorf("armour detail = % x\nwant          % x", f, want)
	}
}
