package markers

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"sync"
	"time"

	"github.com/jdwillmsen/gameops/minecraft/mcmap/internal/chunks"
)

// MaxBody is the most one dimension's markers may come to as JSON. The
// limits on how many there are do not bound this by themselves: a name of
// MaxName characters can take six times as many bytes once escaped.
const MaxBody = 2 << 20

type document struct {
	// At is when the snapshot the markers were read from was taken; absent
	// until there has been one.
	At         *time.Time `json:"at,omitempty"`
	Beds       []Marker   `json:"beds"`
	Containers []Marker   `json:"containers"`
	Mobs       []Marker   `json:"mobs"`
	More       More       `json:"more"`
}

type served struct {
	body []byte
	etag string
}

// Store holds the markers of the last world scanned, already encoded, so
// that serving them is a copy.
type Store struct {
	mu   sync.RWMutex
	docs map[string]served
}

// NewStore is a store that answers with no markers until a scan fills it.
func NewStore() *Store {
	s := &Store{docs: map[string]served{}}
	for _, d := range chunks.Dimensions {
		s.docs[d.Name()] = encode(document{})
	}
	return s
}

// Set replaces every dimension's markers with those of a scan of the
// snapshot taken at at.
func (s *Store) Set(at time.Time, w World) {
	docs := map[string]served{}
	for _, d := range chunks.Dimensions {
		doc := document{At: &at}
		if l := w[d]; l != nil {
			doc.Beds, doc.Containers, doc.Mobs, doc.More = l.Beds, l.Containers, l.Mobs, l.More
		}
		docs[d.Name()] = encode(doc)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.docs = docs
}

// Dimension is the named dimension's markers as JSON, with a tag that
// changes when they do.
func (s *Store) Dimension(name string) (body []byte, etag string, ok bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	d, ok := s.docs[name]
	return d.body, d.etag, ok
}

func encode(doc document) served {
	for {
		// A browser reads an absent list as a broken answer.
		for _, list := range []*[]Marker{&doc.Beds, &doc.Containers, &doc.Mobs} {
			if *list == nil {
				*list = []Marker{}
			}
		}
		body, _ := json.Marshal(doc)
		if len(body) <= MaxBody {
			sum := sha256.Sum256(body)
			return served{body: body, etag: `"` + hex.EncodeToString(sum[:8]) + `"`}
		}
		// Halving ends: with nothing left the answer is a few dozen bytes.
		for _, cut := range []struct {
			list *[]Marker
			more *int
		}{{&doc.Beds, &doc.More.Beds}, {&doc.Containers, &doc.More.Containers}, {&doc.Mobs, &doc.More.Mobs}} {
			keep := len(*cut.list) / 2
			*cut.more += len(*cut.list) - keep
			*cut.list = (*cut.list)[:keep]
		}
	}
}
