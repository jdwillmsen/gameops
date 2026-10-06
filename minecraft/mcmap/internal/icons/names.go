package icons

import (
	"bytes"
	"errors"
	"fmt"
	"regexp"
	"slices"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/jdwillmsen/gameops/minecraft/mcmap/internal/markers"
)

const langPath = "resource_pack/texts/en_US.lang"

// Bounds on the language file. The real one is 0.8 MB in 13,340 lines, of
// which about 180 are names this service uses, the longest 25 characters.
// It is text from outside that ends up in a browser, so it is held to
// these whatever it says, and a name is only ever short plain text.
const (
	maxLangBytes = 4 << 20
	maxLangLines = 100_000
	maxLangNames = 4000
	// MaxNameLength is the longest display name served, in characters.
	MaxNameLength = 64
)

// Which lines of the language file are kept. Its keys follow no one rule:
// an entity is entity.<type>.name, a structure is feature.<kind> with no
// suffix, a block is tile.<name>.name, and the coloured ones are spelt in
// camel case with the game's older word for light grey (item.bed.silver,
// tile.shulkerBoxLightBlue). It names no biome at all.
var (
	langEntity  = regexp.MustCompile(`^entity\.([a-z0-9_]{1,64})\.name$`)
	langFeature = regexp.MustCompile(`^feature\.[a-z0-9_]{1,64}$`)
	langBlock   = regexp.MustCompile(`^(tile\.(chest|trapped_chest|barrel|bed|shulkerBox[A-Za-z]{0,16})|item\.bed\.[A-Za-z]{1,16})\.name$`)
)

func wantedLangKey(key string) bool {
	return langEntity.MatchString(key) || langFeature.MatchString(key) || langBlock.MatchString(key)
}

// cleanLangName is a name from the language file if it is fit to show: one
// line of printable text, no longer than MaxNameLength. Anything else is
// refused whole and not repaired, since the tidied id is a better name
// than half of one.
func cleanLangName(value string) (string, bool) {
	// The file's own comments trail a value after a tab.
	if i := strings.Index(value, "\t#"); i >= 0 {
		value = value[:i]
	}
	value = strings.Trim(value, " ")
	if value == "" || !utf8.ValidString(value) || utf8.RuneCountInString(value) > MaxNameLength {
		return "", false
	}
	for _, r := range value {
		// Controls, the invisible characters that reorder or hide text,
		// the private-use glyphs the game draws button pictures with, and
		// the section sign its colour codes start with.
		if unicode.IsControl(r) || unicode.In(r, unicode.Cf, unicode.Co, unicode.Zl, unicode.Zp) || r == '§' || r == utf8.RuneError {
			return "", false
		}
	}
	return value, true
}

// parseLang reads the names this service uses out of a language file, by
// the file's own keys. A file outside the bounds is refused; a line that
// is not a usable name is passed over.
func parseLang(raw []byte) (map[string]string, error) {
	if len(raw) > maxLangBytes {
		return nil, fmt.Errorf("the language file is %d bytes, over the limit of %d", len(raw), maxLangBytes)
	}
	raw = bytes.TrimPrefix(raw, []byte("\xef\xbb\xbf"))
	out := map[string]string{}
	lines := 0
	for len(raw) > 0 {
		var line []byte
		if end := bytes.IndexByte(raw, '\n'); end >= 0 {
			line, raw = raw[:end], raw[end+1:]
		} else {
			line, raw = raw, nil
		}
		if lines++; lines > maxLangLines {
			return nil, fmt.Errorf("the language file has over %d lines", maxLangLines)
		}
		key, value, found := bytes.Cut(bytes.TrimSuffix(line, []byte("\r")), []byte("="))
		// The longest key kept is well under this; the check keeps a long
		// line from being copied only to be thrown away.
		if !found || len(key) > 128 || len(value) > 4*MaxNameLength+128 || !wantedLangKey(string(key)) {
			continue
		}
		name, ok := cleanLangName(string(value))
		if !ok {
			continue
		}
		out[string(key)] = name
		if len(out) > maxLangNames {
			return nil, fmt.Errorf("the language file holds over %d names", maxLangNames)
		}
	}
	if len(out) == 0 {
		return nil, errors.New("the language file holds none of the names looked for; it is not laid out as expected")
	}
	return out, nil
}

// Names gives everything the map shows its display name: the one the
// game's own language file has for it, or failing that its id tidied into
// words. The zero value, and a nil pointer, know no file and tidy
// everything, which is the state before the file is fetched and the state
// that lasts if it never is.
type Names struct {
	// lang is what the language file said, by its own keys.
	lang map[string]string
	// entities is every entity type the samples define.
	entities []string
}

// NewNames builds the names from what a fetch read.
func NewNames(lang map[string]string, entities []string) *Names {
	return &Names{lang: lang, entities: entities}
}

func (n *Names) from(key string) (string, bool) {
	if n == nil {
		return "", false
	}
	name, ok := n.lang[key]
	return name, ok
}

// Translated is how many names came from the language file.
func (n *Names) Translated() int {
	if n == nil {
		return 0
	}
	return len(n.lang)
}

// Tidy makes an id into words: its namespace and any version suffix
// dropped, underscores to spaces, each word capitalised. villager_v2 is
// Villager and minecraft:mesa_plateau_stone is Mesa Plateau Stone. It is
// what is shown for anything the language file does not list, so that a
// raw id never is. Whatever is passed in, the result is letters, digits
// and single spaces, and no longer than MaxNameLength.
func Tidy(id string) string {
	if i := strings.LastIndexByte(id, ':'); i >= 0 {
		id = id[i+1:]
	}
	words := strings.FieldsFunc(strings.ToValidUTF8(id, ""), func(r rune) bool {
		return !unicode.IsLetter(r) && !unicode.IsDigit(r)
	})
	// The game numbers a type it has reworked: villager_v2, and in file
	// names v1.0, which splits here into v1 and 0.
	for len(words) > 1 && (isVersion(words[len(words)-1]) || (isDigits(words[len(words)-1]) && len(words) > 2 && isVersion(words[len(words)-2]))) {
		words = words[:len(words)-1]
	}
	var b strings.Builder
	length := 0
	for _, word := range words {
		runes := []rune(strings.ToLower(word))
		if length+len(runes)+1 > MaxNameLength {
			break
		}
		if b.Len() > 0 {
			b.WriteByte(' ')
			length++
		}
		runes[0] = unicode.ToUpper(runes[0])
		b.WriteString(string(runes))
		length += len(runes)
	}
	if b.Len() == 0 {
		return "Unknown"
	}
	return b.String()
}

func isDigits(s string) bool {
	return s != "" && strings.Trim(s, "0123456789") == ""
}

func isVersion(word string) bool {
	return (word[0] == 'v' || word[0] == 'V') && isDigits(word[1:])
}

// Entity is the name of a mob or other entity type, as the live layer and
// the markers report it, with or without the minecraft: prefix.
func (n *Names) Entity(kind string) string {
	if name, ok := n.from("entity." + strings.TrimPrefix(kind, "minecraft:") + ".name"); ok {
		return name
	}
	return Tidy(kind)
}

// Container is the name of a kind of container as the markers report it.
func (n *Names) Container(kind string) string {
	key := map[string]string{
		"chest": "tile.chest.name", "trapped_chest": "tile.trapped_chest.name",
		"barrel": "tile.barrel.name", "shulker": "tile.shulkerBox.name",
	}[kind]
	if name, ok := n.from(key); ok {
		return name
	}
	if kind == "shulker" {
		return Tidy("shulker_box")
	}
	return Tidy(kind)
}

// Bed is the name of a bed of a colour, or of a bed of no known colour.
func (n *Names) Bed(colour string) string {
	if !slices.Contains(markers.Colours, colour) {
		if name, ok := n.from("tile.bed.name"); ok {
			return name
		}
		return Tidy("bed")
	}
	if name, ok := n.from("item.bed." + camel(legacyColour(colour), false) + ".name"); ok {
		return name
	}
	return Tidy(colour + "_bed")
}

// Shulker is the name of a shulker box of a colour. An undyed one, and one
// of no known colour, is just a shulker box.
func (n *Names) Shulker(colour string) string {
	if !slices.Contains(markers.Colours, colour) {
		return n.Container("shulker")
	}
	if name, ok := n.from("tile.shulkerBox" + camel(legacyColour(colour), true) + ".name"); ok {
		return name
	}
	return Tidy(colour + "_shulker_box")
}

// featureOf is the language file's word for a structure kind where it is
// not the kind itself. It has none for a witch hut.
var featureOf = map[string]string{"outpost": "pillager_outpost"}

// Structure is the name of a kind of structure.
func (n *Names) Structure(kind string) string {
	feature := kind
	if other, ok := featureOf[kind]; ok {
		feature = other
	}
	if name, ok := n.from("feature." + feature); ok {
		return name
	}
	return Tidy(kind)
}

// BiomeName is the display name of a biome id, with or without the
// minecraft: prefix. The game's language file names no biome, so this is
// the id tidied into words, whatever has or has not been fetched: plains
// is Plains and cold_taiga_hills is Cold Taiga Hills. An id the game has
// kept from an older version reads as that older word (hell, not Nether
// Wastes).
func BiomeName(id string) string {
	return Tidy(id)
}

// Table is every name the page needs, by the ids it holds.
type Table struct {
	// Entities is by mob type, as in the live layer's t and a mob
	// marker's k.
	Entities map[string]string `json:"entities"`
	// Containers is by a container marker's k, and trapped_chest.
	Containers map[string]string `json:"containers"`
	// Beds and Shulkers are by colour. Each also holds default, for one
	// whose colour is not known; Shulkers holds undyed too.
	Beds       map[string]string `json:"beds"`
	Shulkers   map[string]string `json:"shulkers"`
	Structures map[string]string `json:"structures"`
}

// maxTableEntities bounds the entity types in one table: several times
// the 139 the game has.
const maxTableEntities = 1000

// Table is the names of everything the samples define and of each extra
// entity type given, which is how a type seen in the world gets a tidied
// name while the language file is out of reach.
func (n *Names) Table(seen []string) Table {
	t := Table{Entities: map[string]string{}, Containers: map[string]string{}, Beds: map[string]string{}, Shulkers: map[string]string{}, Structures: map[string]string{}}
	var kinds []string
	if n != nil {
		kinds = append(kinds, n.entities...)
		for key := range n.lang {
			if m := langEntity.FindStringSubmatch(key); m != nil {
				kinds = append(kinds, m[1])
			}
		}
	}
	// Sorted, so that which types a full table keeps does not vary.
	slices.Sort(kinds)
	for _, kind := range append(kinds, slices.Sorted(slices.Values(seen))...) {
		kind = strings.TrimPrefix(kind, "minecraft:")
		if len(t.Entities) >= maxTableEntities || !mobType.MatchString(kind) {
			continue
		}
		t.Entities[kind] = n.Entity(kind)
	}
	for _, kind := range ContainerKinds {
		t.Containers[kind] = n.Container(kind)
	}
	t.Beds["default"], t.Shulkers["default"], t.Shulkers[markers.Undyed] = n.Bed(""), n.Shulker(""), n.Shulker(markers.Undyed)
	for _, colour := range markers.Colours {
		t.Beds[colour], t.Shulkers[colour] = n.Bed(colour), n.Shulker(colour)
	}
	for _, kind := range StructureKinds {
		t.Structures[kind] = n.Structure(kind)
	}
	return t
}

// camel turns light_blue into lightBlue, or LightBlue.
func camel(s string, upperFirst bool) string {
	var b strings.Builder
	for i, part := range strings.Split(s, "_") {
		if part == "" {
			continue
		}
		if i > 0 || upperFirst {
			part = strings.ToUpper(part[:1]) + part[1:]
		}
		b.WriteString(part)
	}
	return b.String()
}
