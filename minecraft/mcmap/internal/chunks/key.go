package chunks

// RecordKey is the key of one of a chunk's records, the inverse of RecordOf
// for the records that carry nothing after their tag. It is for looking up
// one chunk's record without walking the world to it.
func RecordKey(p Pos, tag byte) []byte {
	k := make([]byte, 0, 13)
	k = appendInt32(k, p.X)
	k = appendInt32(k, p.Z)
	if p.Dim != Overworld {
		k = appendInt32(k, int32(p.Dim))
	}
	return append(k, tag)
}

func appendInt32(b []byte, v int32) []byte {
	return append(b, byte(v), byte(v>>8), byte(v>>16), byte(v>>24))
}
