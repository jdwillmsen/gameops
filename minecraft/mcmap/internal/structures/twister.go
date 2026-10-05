package structures

// twister is MT19937, the generator the game seeds once per region. Only
// the first few numbers of each seeding are ever drawn, so it produces them
// one at a time from the seeded state instead of regenerating the whole
// state first: a number needs its own word, the next, and the one 397 on.
type twister struct {
	state [twisterWords]uint32
	drawn int
}

const (
	twisterWords = 624
	twisterShift = 397
	// maxDraws is how many numbers one seeding can give this way: past it
	// the word 397 on has itself been replaced.
	maxDraws = twisterWords - twisterShift
)

func newTwister(seed uint32) *twister {
	t := &twister{}
	t.state[0] = seed
	for i := 1; i < twisterWords; i++ {
		prev := t.state[i-1]
		t.state[i] = 1812433253*(prev^(prev>>30)) + uint32(i)
	}
	return t
}

func (t *twister) next() uint32 {
	if t.drawn >= maxDraws {
		panic("structures: more numbers drawn from one seeding than this generator gives")
	}
	k := t.drawn
	t.drawn++
	y := (t.state[k] & 0x80000000) | (t.state[k+1] & 0x7fffffff)
	v := t.state[k+twisterShift] ^ (y >> 1)
	if y&1 != 0 {
		v ^= 0x9908b0df
	}
	v ^= v >> 11
	v ^= (v << 7) & 0x9d2c5680
	v ^= (v << 15) & 0xefc60000
	v ^= v >> 18
	return v
}
