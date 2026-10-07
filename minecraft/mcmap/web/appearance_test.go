package web

import (
	"bytes"
	"fmt"
	"math"
	"regexp"
	"strconv"
	"strings"
	"testing"
)

// The colours of a theme, as the stylesheet names them: the first set, with
// a theme's own laid over it.
func themeColours(t *testing.T, css []byte, theme string) map[string]string {
	t.Helper()
	out := map[string]string{}
	take := func(selector string) {
		m := regexp.MustCompile(`(?s)\n` + regexp.QuoteMeta(selector) + ` \{(.*?)\n\}`).FindSubmatch(css)
		if m == nil {
			t.Fatalf("style.css has no block for %s", selector)
		}
		for _, d := range regexp.MustCompile(`--([a-z-]+): ([^;]+);`).FindAllSubmatch(m[1], -1) {
			out[string(d[1])] = string(d[2])
		}
	}
	take(":root")
	if theme != "" {
		take(`:root[data-theme="` + theme + `"]`)
	}
	return out
}

type rgba struct{ r, g, b, a float64 }

func colourOf(t *testing.T, value string) rgba {
	t.Helper()
	if m := regexp.MustCompile(`^#([0-9a-f]{2})([0-9a-f]{2})([0-9a-f]{2})$`).FindStringSubmatch(value); m != nil {
		var c [3]float64
		for i := range c {
			n, _ := strconv.ParseUint(m[i+1], 16, 8)
			c[i] = float64(n)
		}
		return rgba{c[0], c[1], c[2], 1}
	}
	if m := regexp.MustCompile(`^rgba\((\d+), (\d+), (\d+), ([0-9.]+)\)$`).FindStringSubmatch(value); m != nil {
		var c [4]float64
		for i := range c {
			c[i], _ = strconv.ParseFloat(m[i+1], 64)
		}
		return rgba{c[0], c[1], c[2], c[3]}
	}
	t.Fatalf("style.css has a colour in a form this test does not read: %s", value)
	return rgba{}
}

func (top rgba) over(under rgba) rgba {
	mix := func(a, b float64) float64 { return a*top.a + b*(1-top.a) }
	return rgba{mix(top.r, under.r), mix(top.g, under.g), mix(top.b, under.b), 1}
}

// The ratio WCAG measures contrast by.
func contrast(a, b rgba) float64 {
	lum := func(c rgba) float64 {
		f := func(v float64) float64 {
			v /= 255
			if v <= 0.03928 {
				return v / 12.92
			}
			return math.Pow((v+0.055)/1.055, 2.4)
		}
		return 0.2126*f(c.r) + 0.7152*f(c.g) + 0.0722*f(c.b)
	}
	hi, lo := lum(a), lum(b)
	if hi < lo {
		hi, lo = lo, hi
	}
	return (hi + 0.05) / (lo + 0.05)
}

// The light and the high-contrast themes are for whoever cannot read the
// dark one, so their text is held to four and a half to one against what
// it is written on, and the edges that tell a control or a marker from its
// ground to three to one. What is drawn on the map is measured over the
// lightest and the darkest terrain there is.
func TestLightAndHighContrastThemesMeetTheContrastTheyAreFor(t *testing.T) {
	css := read(t, "style.css")
	terrain := []rgba{{0, 0, 0, 1}, {255, 255, 255, 1}}
	rings := []string{"live-players", "live-hostile", "live-passive", "live-villager", "live-other", "live-me",
		"marker-waypoints", "marker-beds", "marker-containers", "marker-mobs", "named"}
	for _, theme := range []string{"light", "contrast"} {
		c := themeColours(t, css, theme)
		get := func(name string) rgba {
			v, ok := c[name]
			if !ok {
				t.Fatalf("the %s theme has no --%s", theme, name)
			}
			return colourOf(t, v)
		}
		least := map[string]float64{"text": math.Inf(1), "edge": math.Inf(1)}
		check := func(kind, what string, got, want float64) {
			least[kind] = math.Min(least[kind], got)
			if got < want {
				t.Errorf("%s theme: %s is %.2f to one, want at least %.1f", theme, what, got, want)
			}
		}
		for _, fg := range []string{"text", "dim", "accent", "warn"} {
			for _, bg := range []string{"bg", "panel"} {
				check("text", fg+" on "+bg, contrast(get(fg), get(bg)), 4.5)
			}
			for _, under := range terrain {
				float := get("float").over(under)
				check("text", fg+" on a panel over the map", contrast(get(fg), float), 4.5)
			}
		}
		check("text", "on-accent on accent", contrast(get("on-accent"), get("accent")), 4.5)
		for _, under := range terrain {
			plate := get("label-plate").over(under)
			for _, fg := range []string{"text", "named-text", "waypoint-text", "me-text"} {
				check("text", fg+" on a label's plate", contrast(get(fg), plate), 4.5)
			}
			backing := get("marker-backing").over(under)
			for _, ring := range rings {
				check("edge", ring+" on a marker's backing", contrast(get(ring), backing), 3)
			}
		}
		for _, ring := range rings {
			check("edge", ring+" against a marker's outline", contrast(get(ring), get("marker-ink")), 3)
		}
		for _, bg := range []string{"bg", "panel"} {
			check("edge", "line on "+bg, contrast(get("line"), get(bg)), 3)
			check("edge", "accent on "+bg, contrast(get("accent"), get(bg)), 3)
		}
		t.Logf("%s theme: lowest text %.2f, lowest edge %.2f", theme, least["text"], least["edge"])
	}
}

// A colour written into a rule does not follow the theme. Each has a name
// in the blocks at the top, and every rule and every canvas uses the name.
func TestEveryColourOnThePageIsOneOfTheThemes(t *testing.T) {
	css := read(t, "style.css")
	body := css[bytes.Index(css, []byte(`:root[data-text="small"]`)):]
	if len(body) == len(css) {
		t.Fatal("style.css no longer has its themes at the top")
	}
	colour := regexp.MustCompile(`#[0-9a-fA-F]{3,8}\b|\brgba?\(|\bhsla?\(`)
	for i, line := range bytes.Split(body, []byte("\n")) {
		// A backdrop does not take the page's colours in every browser.
		if bytes.Contains(line, []byte(".sheet::backdrop")) {
			continue
		}
		if colour.Match(line) {
			t.Errorf("style.css has a colour outside the themes: %s (line %d after them)", bytes.TrimSpace(line), i+1)
		}
	}
	defined := themeColours(t, css, "")
	for _, m := range regexp.MustCompile(`var\(--([a-z-]+)`).FindAllSubmatch(css, -1) {
		name := string(m[1])
		if _, ok := defined[name]; !ok && name != "kind" && name != "inspect-height" {
			t.Errorf("style.css uses --%s, which no theme defines", name)
		}
	}
	// What a script draws on a canvas it draws in the theme's colours, by
	// name, and a name the stylesheet does not have would be no colour.
	asked := regexp.MustCompile("(?:colour|themed)\\((?:'([a-z-]+)'|`marker-\\$\\{kind\\}`)")
	for _, script := range []string{"app.js", "icons.js", "live.js", "markers.js", "trails.js", "slime.js", "chunk.js"} {
		found := asked.FindAllSubmatch(read(t, script), -1)
		if len(found) == 0 {
			t.Errorf("%s no longer draws in the theme's colours", script)
		}
		for _, m := range found {
			if _, ok := defined[string(m[1])]; len(m[1]) > 0 && !ok {
				t.Errorf("%s asks for the colour %s, which no theme defines", script, m[1])
			}
		}
	}
	for _, kind := range []string{"waypoints", "beds", "containers", "mobs"} {
		if _, ok := defined["marker-"+kind]; !ok {
			t.Errorf("no theme defines the colour of the %s markers", kind)
		}
	}
	for _, theme := range []string{"light", "contrast"} {
		own := themeColours(t, css, theme)
		for name := range own {
			if _, ok := defined[name]; !ok {
				t.Errorf("the %s theme sets --%s, which the first theme does not have", theme, name)
			}
		}
	}
	// A change of theme or size redraws what is on the canvas from the
	// pictures already fetched.
	icons := read(t, "icons.js")
	if !regexp.MustCompile(`composed = composedAs\(\);\s*sprites\.clear\(\);\s*tags\.clear\(\);\s*paint\(document\);`).Match(icons) || bytes.Contains(icons, []byte("bitmaps.clear()")) {
		t.Error("icons.js no longer composes its sprites anew, from the pictures it has, when the look changes")
	}
	for _, script := range []string{"live.js", "markers.js"} {
		if !regexp.MustCompile(`styled = styledAs\(\);\s*restyle\(\);`).Match(read(t, script)) {
			t.Errorf("%s no longer restyles its markers when the look changes", script)
		}
		restyle := regexp.MustCompile(`(?s)function restyle\(\) \{.*?\n  \}`).Find(read(t, script))
		if bytes.Contains(restyle, []byte("fetch(")) || bytes.Contains(restyle, []byte("refresh(")) || bytes.Contains(restyle, []byte("sync(")) {
			t.Errorf("%s asks the server for something when only the look has changed", script)
		}
	}
}

// Every appearance setting has a control, each control one of the values
// the setting may take, and one button puts them all back.
func TestEveryAppearanceSettingHasAControlAndADefault(t *testing.T) {
	settings := read(t, "settings.js")
	block := regexp.MustCompile(`(?s)const LOOK_DEFAULTS = Object\.freeze\(\{(.*?)\}\);`).FindSubmatch(settings)
	if block == nil {
		t.Fatal("settings.js no longer lists the default of each appearance setting")
	}
	defaults := map[string]string{}
	for _, m := range regexp.MustCompile(`(\w+): '?([\w]+)'?,`).FindAllSubmatch(block[1], -1) {
		defaults[string(m[1])] = string(m[2])
	}
	if len(defaults) != 14 {
		t.Fatalf("read %d appearance settings from settings.js, want 14", len(defaults))
	}
	// The page opens as it always has until the viewer chooses otherwise.
	for name, want := range map[string]string{"theme": "dark", "size": "normal", "opacityBiomes": "60", "motion": "system", "labelPlayers": "always"} {
		if defaults[name] != want {
			t.Errorf("the default %s is %s, want %s", name, defaults[name], want)
		}
	}
	page := read(t, "index.html")
	for name, value := range defaults {
		control := regexp.MustCompile(`(?s)<(select|input)[^>]*data-look="` + name + `"[^>]*>(.*?)(</select>|</span>)`).FindSubmatch(page)
		if control == nil {
			t.Errorf("index.html has no control for the appearance setting %s", name)
			continue
		}
		if string(control[1]) == "select" && !bytes.Contains(control[2], []byte(`<option value="`+value+`">`)) {
			t.Errorf("the control for %s does not offer its default, %s", name, value)
		}
		if !regexp.MustCompile(`\b` + name + `: `).Match(regexp.MustCompile(`(?s)const LOOK = \{.*?\n  \};`).Find(settings)) {
			t.Errorf("settings.js has a default for %s and no description of what it may be", name)
		}
	}
	for _, theme := range []string{"system", "dark", "light", "contrast"} {
		if !bytes.Contains(page, []byte(fmt.Sprintf(`<option value="%s">`, theme))) {
			t.Errorf("index.html does not offer the %s theme", theme)
		}
	}
	js := read(t, "appearance.js")
	for _, need := range []string{
		"option.textContent += ' (default)';",
		"settings.set('look', { ...DEFAULTS });",
		"el.reset.disabled = Object.keys(DEFAULTS).every((key) => look[key] === DEFAULTS[key]);",
		"if (el.open.offsetParent === null && el.more) el.more.focus();",
	} {
		if !bytes.Contains(js, []byte(need)) {
			t.Errorf("appearance.js no longer has %s", need)
		}
	}
	if !bytes.Contains(page, []byte(`<button id="appearance-reset" type="button">Reset to defaults</button>`)) {
		t.Error("index.html has no button to put the appearance back to its defaults")
	}
	// Pixel art is only ever enlarged by a whole number of screen pixels.
	icons := read(t, "icons.js")
	if !bytes.Contains(icons, []byte("const BIG = DENSITY === 2 ? 24 : 32;")) || !bytes.Contains(icons, []byte("ctx.imageSmoothingEnabled = icon * DENSITY < drawn.width && icon < ICON;")) {
		t.Error("icons.js no longer enlarges a picture whole, or blends one it enlarges")
	}
	// Less motion is the viewer's choice where one is made, and the
	// system's where it is not; the map itself is told as well.
	css := read(t, "style.css")
	for _, need := range []string{
		`:root[data-motion="reduce"] .found::after { animation: none; }`,
		`@media (prefers-reduced-motion: reduce) { :root:not([data-motion]) .found::after { animation: none; } }`,
		`:root[data-density="compact"] body { font-size: 13px; }`,
		`.leaflet-tooltip { font-size: var(--label-size); }`,
	} {
		if !bytes.Contains(css, []byte(need)) {
			t.Errorf("style.css no longer has %s", need)
		}
	}
	if !strings.Contains(string(read(t, "app.js")), "map._zoomAnimated = animated.zoom && !still;") {
		t.Error("app.js no longer stills the map's own animations for whoever asked for less motion")
	}
}
