package live

import (
	"testing"
	"time"
)

type sightings []string

func (s *sightings) Seen(id, name string, _ time.Time) { *s = append(*s, id+"="+name) }

func TestLayerTellsTheRosterOfEachPlayerInASample(t *testing.T) {
	l := New(10*time.Second, 1000, nil)
	var seen sightings
	l.Roster = &seen
	l.Ingest(raw(1, t0, `{"gen":1,"kind":"players","dim":"overworld","part":0,"parts":1,"more":0,"items":[`+
		`{"i":"-1","n":"Alpha","x":0,"y":64,"z":0},{"i":"2","n":"Beta","x":1,"y":64,"z":1}]}`), t0)
	l.Ingest(raw(2, t0, surfaced), t0)
	if len(seen) != 2 || seen[0] != "-1=Alpha" || seen[1] != "2=Beta" {
		t.Fatalf("roster was told %v", seen)
	}
}
