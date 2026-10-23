package chunks

import "testing"

func TestRecordOf(t *testing.T) {
	for _, c := range []struct {
		name string
		key  []byte
		pos  Pos
		tag  byte
		ok   bool
	}{
		{"overworld block entities", chunkKey(0, 3, -7, 0x31), Pos{Overworld, 3, -7}, 0x31, true},
		{"nether block entities", chunkKey(1, 10, 20, 0x31), Pos{Nether, 10, 20}, 0x31, true},
		{"end subchunk", chunkKey(2, -5, 0, 0x2f, 0xfc), Pos{End, -5, 0}, 0x2f, true},
		{"a named record", []byte("Overworld"), Pos{}, 0, false},
		{"an actor", []byte("actorprefix12345678"), Pos{}, 0, false},
	} {
		pos, tag, ok := RecordOf(c.key)
		if pos != c.pos || tag != c.tag || ok != c.ok {
			t.Errorf("%s: RecordOf(%v) = %+v, %v, %v", c.name, c.key, pos, tag, ok)
		}
	}
}
