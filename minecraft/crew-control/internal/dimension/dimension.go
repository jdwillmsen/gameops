// Package dimension turns the dimension id a unit reports into the closed
// name the console, the metrics and the stored events use. The id is the
// unit's own word, so nothing outside this list reaches a label or a public
// sentence.
package dimension

const (
	Overworld = "overworld"
	Nether    = "nether"
	End       = "end"
	// Other is every id this controller does not know, such as a custom
	// dimension from a newer server.
	Other = "other"
)

// Names lists every value Name can return, in gauge order.
var Names = []string{Overworld, Nether, End, Other}

// Name maps a Bedrock dimension id to its closed name.
func Name(id int32) string {
	switch id {
	case 0:
		return Overworld
	case 1:
		return Nether
	case 2:
		return End
	}
	return Other
}

// Gauge is the number a name is exported as: its place in Names, so the
// first three match the game's own ids and every unknown id shares the last.
// A name outside the list reads as Other.
func Gauge(name string) float64 {
	for i, n := range Names {
		if n == name {
			return float64(i)
		}
	}
	return float64(len(Names) - 1)
}
