package icons

import (
	"bytes"
	"fmt"
	"maps"
	"slices"
	"strings"
	"testing"
	"unicode"

	"github.com/jdwillmsen/gameops/minecraft/mcmap/internal/markers"
)

func namesOf(t testing.TB, lang string, entities ...string) *Names {
	t.Helper()
	parsed, err := parseLang([]byte(lang))
	if err != nil {
		t.Fatal(err)
	}
	return NewNames(parsed, entities)
}

func TestNamesComeFromTheLanguageFileByItsOwnKeyForms(t *testing.T) {
	n := namesOf(t, syntheticLang)
	for got, want := range map[string]string{
		// The type the game gave a reworked villager is in the file as it is.
		n.Entity("villager_v2"):           "Synthetic Villager",
		n.Entity("minecraft:cow"):         "Synthetic Cow",
		n.Entity("evocation_illager"):     "Synthetic Evoker",
		n.Container("chest"):              "Synthetic Chest",
		n.Container("trapped_chest"):      "Synthetic Trapped Chest",
		n.Container("barrel"):             "Synthetic Barrel",
		n.Container("shulker"):            "Synthetic Shulker Box",
		n.Bed(""):                         "Synthetic Bed",
		n.Bed("red"):                      "Synthetic Red Bed",
		n.Bed("light_gray"):               "Synthetic Light Gray Bed",
		n.Bed("light_blue"):               "Synthetic Light Blue Bed",
		n.Shulker("light_gray"):           "Synthetic Light Gray Shulker Box",
		n.Shulker("light_blue"):           "Synthetic Light Blue Shulker Box",
		n.Shulker(markers.Undyed):         "Synthetic Shulker Box",
		n.Shulker(""):                     "Synthetic Shulker Box",
		n.Structure("fortress"):           "Synthetic Fortress",
		n.Structure("outpost"):            "Synthetic Outpost",
		n.Structure("village"):            "Synthetic Village",
		n.Entity("cow") + n.Entity("cow"): "Synthetic CowSynthetic Cow",
	} {
		if got != want {
			t.Errorf("got %q, want %q", got, want)
		}
	}
	if n.Translated() != 16 {
		t.Errorf("%d names kept from the file, want 16", n.Translated())
	}
}

func TestNamesFallBackToATidiedID(t *testing.T) {
	n := namesOf(t, syntheticLang)
	for got, want := range map[string]string{
		n.Entity("zombie_villager_v2"): "Zombie Villager",
		n.Entity("happy_ghast"):        "Happy Ghast",
		n.Structure("witch_hut"):       "Witch Hut",
		n.Structure("monument"):        "Monument",
		n.Bed("purple"):                "Purple Bed",
		n.Shulker("purple"):            "Purple Shulker Box",
		n.Container("hopper"):          "Hopper",
		n.Bed("plaid"):                 "Synthetic Bed",
	} {
		if got != want {
			t.Errorf("got %q, want %q", got, want)
		}
	}
	// With no file at all, and with no Names at all, everything is tidied.
	var none *Names
	for _, n := range []*Names{none, {}, NewNames(nil, nil)} {
		for got, want := range map[string]string{
			n.Entity("villager_v2"):   "Villager",
			n.Container("shulker"):    "Shulker Box",
			n.Container("chest"):      "Chest",
			n.Bed(""):                 "Bed",
			n.Bed("light_gray"):       "Light Gray Bed",
			n.Shulker(markers.Undyed): "Shulker Box",
			n.Structure("outpost"):    "Outpost",
		} {
			if got != want {
				t.Errorf("with no language file: got %q, want %q", got, want)
			}
		}
		if n.Translated() != 0 {
			t.Error("names counted as read from a file there was none of")
		}
	}
}

func TestTidy(t *testing.T) {
	for in, want := range map[string]string{
		"villager_v2":                  "Villager",
		"zombie_villager_v2":           "Zombie Villager",
		"minecraft:evocation_illager":  "Evocation Illager",
		"shulker_v1.0":                 "Shulker",
		"cold_taiga_hills":             "Cold Taiga Hills",
		"minecraft:mesa_plateau_stone": "Mesa Plateau Stone",
		"the_end":                      "The End",
		"tnt":                          "Tnt",
		"v2":                           "V2",
		"area_51":                      "Area 51",
		"custom:my.mob-thing":          "My Mob Thing",
		"":                             "Unknown",
		"___":                          "Unknown",
		"<script>alert(1)</script>":    "Script Alert 1 Script",
		"a\x00b\tc\nd":                 "A B C D",
		"bad\xffbytes":                 "Badbytes",
		"ALLCAPS_id":                   "Allcaps Id",
	} {
		if got := Tidy(in); got != want {
			t.Errorf("Tidy(%q) = %q, want %q", in, got, want)
		}
	}
	long := Tidy(strings.Repeat("word_", 100))
	if n := len([]rune(long)); n > MaxNameLength || !strings.HasPrefix(long, "Word Word") || strings.HasSuffix(long, " ") {
		t.Errorf("a long id tidied to %d characters: %q", n, long)
	}
}

func TestBiomeNameIsATidiedID(t *testing.T) {
	for in, want := range map[string]string{
		"plains": "Plains", "minecraft:cherry_grove": "Cherry Grove", "extreme_hills_plus_trees_mutated": "Extreme Hills Plus Trees Mutated",
		"deep_dark": "Deep Dark", "": "Unknown",
	} {
		if got := BiomeName(in); got != want {
			t.Errorf("BiomeName(%q) = %q, want %q", in, got, want)
		}
	}
}

// plain reports whether a name is fit to show as one: words, and not an id.
func plain(name string) bool {
	if name == "" || len([]rune(name)) > MaxNameLength || strings.TrimSpace(name) != name {
		return false
	}
	for _, r := range name {
		if r == '_' || r == ':' || unicode.IsControl(r) {
			return false
		}
	}
	return unicode.IsUpper([]rune(name)[0]) || unicode.IsDigit([]rune(name)[0])
}

// Whatever the file lists and whatever the world reports, nothing in the
// table is an id: with the file, without it, and for types nobody has
// heard of.
func TestNoRawIDIsEverServedAsAName(t *testing.T) {
	seen := []string{"villager_v2", "zombie_villager_v2", "minecraft:happy_ghast", "armor_stand", "xp_orb", "some_new_mob_v3", "a", "ender_dragon"}
	defined := []string{"cow", "villager_v2", "bed", "decorated_pot", "trial_spawner"}
	for name, n := range map[string]*Names{"with the file": namesOf(t, syntheticLang, defined...), "without it": NewNames(nil, defined), "with nothing fetched": nil} {
		table := n.Table(seen)
		for _, kind := range seen {
			kind = strings.TrimPrefix(kind, "minecraft:")
			if got, ok := table.Entities[kind]; !ok || got == kind || !plain(got) {
				t.Errorf("%s: %s is named %q", name, kind, got)
			}
		}
		for group, names := range map[string]map[string]string{"entities": table.Entities, "containers": table.Containers, "beds": table.Beds, "shulkers": table.Shulkers, "structures": table.Structures} {
			if len(names) == 0 {
				t.Errorf("%s: no %s named", name, group)
			}
			for id, got := range names {
				if !plain(got) || (got == id && strings.ContainsAny(id, "_:")) {
					t.Errorf("%s: %s %q is named %q", name, group, id, got)
				}
			}
		}
		// Every key the page can hold has a name waiting for it.
		for _, colour := range append([]string{"default"}, markers.Colours...) {
			if table.Beds[colour] == "" || table.Shulkers[colour] == "" {
				t.Errorf("%s: colour %s has no bed or no shulker box name", name, colour)
			}
		}
		if got := slices.Sorted(maps.Keys(table.Containers)); !slices.Equal(got, []string{"barrel", "chest", "shulker", "trapped_chest"}) {
			t.Errorf("%s: containers named are %v", name, got)
		}
		if got := slices.Sorted(maps.Keys(table.Structures)); !slices.Equal(got, []string{"fortress", "monument", "outpost", "stronghold", "trial_chamber", "village", "witch_hut"}) {
			t.Errorf("%s: structures named are %v", name, got)
		}
	}
	// An id that is not one is not given an entry to be looked up by.
	if table := (*Names)(nil).Table([]string{"<script>", "UPPER", strings.Repeat("a", 65), ""}); len(table.Entities) != 0 {
		t.Errorf("entities = %v, want none", table.Entities)
	}
	var many []string
	for i := range maxTableEntities * 2 {
		many = append(many, fmt.Sprintf("mob_%d", i))
	}
	if table := (*Names)(nil).Table(many); len(table.Entities) != maxTableEntities {
		t.Errorf("%d entities in one table, want no more than %d", len(table.Entities), maxTableEntities)
	}
}

func TestParseLangKeepsOnlyNamesFitToShow(t *testing.T) {
	lang := "\xef\xbb\xbfentity.first.name=First\r\n" +
		"entity.spaced.name=  Spaced Out  \n" +
		"entity.commented.name=Commented\t## why it is called that\n" +
		"entity.control.name=Bell\x07Ringer\n" +
		"entity.escape.name=Esc\x1b[31mRed\n" +
		"entity.tab.name=Tab\tSeparated\n" +
		"entity.bidi.name=Evil\u202eretsnoM\n" +
		"entity.zerowidth.name=Zero\u200bWidth\n" +
		"entity.glyph.name=Press \ue000 to ride\n" +
		"entity.coloured.name=§cRed Mob\n" +
		"entity.invalid.name=Bad\xffBytes\n" +
		"entity.empty.name=\n" +
		"entity.long.name=" + strings.Repeat("é", MaxNameLength+1) + "\n" +
		"entity.longest.name=" + strings.Repeat("é", MaxNameLength) + "\n" +
		"entity.markup.name=<b>Bold & Brash</b>\n" +
		"entity.Upper.name=Wrong Key\n" +
		"entity.no.suffix=Wrong Key\n" +
		"gui.something=Not Wanted\n" +
		"tile.furnace.name=Not Wanted\n" +
		"no equals sign here\n" +
		"entity.repeated.name=First\nentity.repeated.name=Last\n" +
		"entity.last.name=No newline at the end"
	got, err := parseLang([]byte(lang))
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]string{
		"entity.first.name": "First", "entity.spaced.name": "Spaced Out", "entity.commented.name": "Commented",
		"entity.longest.name": strings.Repeat("é", MaxNameLength),
		// Markup is only text here; the page must write it as text.
		"entity.markup.name":   "<b>Bold & Brash</b>",
		"entity.repeated.name": "Last", "entity.last.name": "No newline at the end",
	}
	if !maps.Equal(got, want) {
		for key, name := range got {
			if want[key] != name {
				t.Errorf("kept %s = %q", key, name)
			}
		}
		for key := range want {
			if _, ok := got[key]; !ok {
				t.Errorf("dropped %s", key)
			}
		}
	}
}

func TestParseLangRefusesAFileOutsideItsBounds(t *testing.T) {
	line := "entity.cow.name=Cow\n"
	var manyNames bytes.Buffer
	for i := range maxLangNames + 1 {
		fmt.Fprintf(&manyNames, "entity.mob_%d.name=Mob\n", i)
	}
	for name, raw := range map[string][]byte{
		"too large":           append([]byte(line), bytes.Repeat([]byte("x"), maxLangBytes)...),
		"too many lines":      append([]byte(line), bytes.Repeat([]byte("\n"), maxLangLines)...),
		"too many names":      manyNames.Bytes(),
		"none of the names":   []byte("gui.ok=OK\nmenu.play=Play\n"),
		"not a language file": []byte("<html><body>rate limited</body></html>"),
		"empty":               {},
	} {
		if got, err := parseLang(raw); err == nil {
			t.Errorf("%s: kept %d names", name, len(got))
		}
	}
	// A long line that is not a name costs nothing and stops nothing.
	long := "howtoplay.text=" + strings.Repeat("words ", 100_000) + "\n" + line + "entity.pig.name=" + strings.Repeat("x", 1<<20) + "\n"
	if got, err := parseLang([]byte(long)); err != nil || len(got) != 1 || got["entity.cow.name"] != "Cow" {
		t.Errorf("beside a long line: %v, %v", got, err)
	}
}

func TestFetchReadsTheNamesAndLeavesThemOutWhenTheFileIsNotUsable(t *testing.T) {
	s := newSamples(t)
	set, err := s.source().Fetch(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if set.Lang["entity.villager_v2.name"] != "Synthetic Villager" || set.Lang["feature.fortress"] != "Synthetic Fortress" || len(set.Lang) != 16 {
		t.Errorf("names = %v", set.Lang)
	}
	// Every type the samples define, with an egg or without, so that each
	// has a name to be looked up.
	if !slices.Contains(set.Entities, "armor_stand") || !slices.Contains(set.Entities, "cow") {
		t.Errorf("entities = %v", set.Entities)
	}

	for name, body := range map[string][]byte{
		"absent":              nil,
		"over the byte limit": bytes.Repeat([]byte("entity.cow.name=Cow\n"), maxLangBytes/20+1),
		"too many lines":      append([]byte("entity.cow.name=Cow\n"), bytes.Repeat([]byte("\n"), maxLangLines)...),
		"something else":      []byte("<html>rate limited</html>"),
	} {
		t.Run(name, func(t *testing.T) {
			s := newSamples(t)
			s.mu.Lock()
			if body == nil {
				delete(s.files, langPath)
			} else {
				s.files[langPath] = body
			}
			s.mu.Unlock()
			set, err := s.source().Fetch(t.Context())
			if err != nil {
				t.Fatalf("the fetch failed for the language file: %v", err)
			}
			if len(set.Lang) != 0 || !slices.Equal(set.Missing, []string{langPath}) {
				t.Errorf("names = %v, missing = %v", set.Lang, set.Missing)
			}
			if len(set.Mobs) != 3 || len(set.Pictures) != len(everyPicture()) {
				t.Errorf("%d mob icons and %d pictures: the rest did not survive", len(set.Mobs), len(set.Pictures))
			}
			if got := NewNames(set.Lang, set.Entities).Entity("villager_v2"); got != "Villager" {
				t.Errorf("without the file a villager is %q", got)
			}
		})
	}
}
