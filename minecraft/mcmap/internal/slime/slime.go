// Package slime says which chunks are slime chunks.
//
// On Bedrock that depends on nothing but the chunk's coordinates: the same
// chunks are slime chunks in every world, whatever its seed. The function
// here is the one the game's own was found to be when it was taken apart
// (by protolambda and jocopa3, simplified by hhhxiao), and is the one every
// Bedrock slime finder uses.
package slime

// Chunk reports whether the chunk at chunk coordinates cx, cz is a slime
// chunk: one where slimes spawn below y 40 whatever the biome.
//
// The game seeds a Mersenne Twister (MT19937) with cx*0x1f1f1f1f XOR cz,
// in 32-bit arithmetic, draws one number, and calls the chunk a slime
// chunk when that number is a multiple of ten. One chunk in ten is.
func Chunk(cx, cz int32) bool {
	return first(uint32(cx)*0x1f1f1f1f^uint32(cz))%10 == 0
}

// first is the first number MT19937 gives for a seed. That number is made
// from the seeded state's words 0, 1 and 397 alone, so the state is filled
// only that far and never regenerated.
func first(seed uint32) uint32 {
	var state [398]uint32
	state[0] = seed
	for i := 1; i < len(state); i++ {
		prev := state[i-1]
		state[i] = 1812433253*(prev^(prev>>30)) + uint32(i)
	}
	y := state[0]&0x80000000 | state[1]&0x7fffffff
	v := state[397] ^ y>>1
	if y&1 != 0 {
		v ^= 0x9908b0df
	}
	v ^= v >> 11
	v ^= v << 7 & 0x9d2c5680
	v ^= v << 15 & 0xefc60000
	v ^= v >> 18
	return v
}
