package chunks

import (
	"bytes"
	"testing"
)

func TestRecordKeyIsWhatRecordOfReads(t *testing.T) {
	for _, p := range []Pos{{Overworld, 3, -7}, {Nether, -1, 0}, {End, 1 << 20, -(1 << 20)}} {
		k := RecordKey(p, 0x39)
		if !bytes.Equal(k, chunkKey(int32(p.Dim), p.X, p.Z, 0x39)) {
			t.Errorf("RecordKey(%+v) = %x", p, k)
		}
		got, tag, ok := RecordOf(k)
		if !ok || got != p || tag != 0x39 {
			t.Errorf("RecordOf(%x) = %+v, %#x, %v", k, got, tag, ok)
		}
	}
}
