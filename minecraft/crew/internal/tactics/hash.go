package tactics

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"maps"
)

// Hash is the identity a profile is approved, pushed and confirmed by: the
// SHA-256 of the validated profile's canonical JSON, lineage included. It
// never writes to the profile it is given, because a unit hashes the profile
// its fight is reading from another goroutine and Validate clamps the
// overrides in place.
func Hash(pr Profile) (string, error) {
	pr.Overrides = maps.Clone(pr.Overrides)
	pr, err := pr.Validate()
	if err != nil {
		return "", err
	}
	b, err := json.Marshal(pr)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:]), nil
}
