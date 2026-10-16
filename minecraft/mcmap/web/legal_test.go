package web

import (
	"bytes"
	"regexp"
	"strings"
	"testing"
)

// The game's publisher asks every site about the game to say this, in
// these words, where a visitor meets the site and where they look for help.
func TestThePageSaysItIsNotAnOfficialProduct(t *testing.T) {
	const words = "NOT AN OFFICIAL MINECRAFT PRODUCT. NOT APPROVED BY OR ASSOCIATED WITH MOJANG OR MICROSOFT."
	page := read(t, "index.html")
	login := regexp.MustCompile(`(?s)<section id="login".*?</section>`).Find(page)
	help := regexp.MustCompile(`(?s)<dialog id="help".*?</dialog>`).Find(page)
	if !bytes.Contains(login, []byte(`<p class="legal">`+words+`</p>`)) {
		t.Error("the login screen no longer says the page is not an official product")
	}
	if !bytes.Contains(help, []byte(`<p class="legal">`+words+`</p>`)) {
		t.Error("the help no longer says the page is not an official product")
	}
	if !bytes.Contains(read(t, "style.css"), []byte(".legal { margin: var(--sheet-pad) 0 0; color: var(--dim); font-size: var(--caption); line-height: var(--caption-lh); }")) {
		t.Error("style.css no longer writes it small and legible")
	}
}

// The pages about the map are read before a login and say the same thing
// the map does, and every place a visitor meets the map links to them.
var aboutPages = []string{"about", "guide", "privacy", "terms", "credits"}

func TestEveryAboutPageSaysItIsNotAnOfficialProductAndLinksToTheRest(t *testing.T) {
	const words = "NOT AN OFFICIAL MINECRAFT PRODUCT. NOT APPROVED BY OR ASSOCIATED WITH MOJANG OR MICROSOFT."
	for _, name := range aboutPages {
		page := read(t, name+".html")
		if !bytes.Contains(page, []byte(`<p class="legal">`+words+`</p>`)) {
			t.Errorf("%s does not carry the disclaimer", name)
		}
		for _, other := range aboutPages {
			if !bytes.Contains(page, []byte(`href="`+other+`"`)) {
				t.Errorf("%s does not link to %s", name, other)
			}
		}
		if !bytes.Contains(page, []byte(`href="./"`)) {
			t.Errorf("%s has no way back to the map", name)
		}
		if !bytes.Contains(page, []byte(`Questions? Email <a href="mailto:{{contact}}">{{contact}}</a>.`)) {
			t.Errorf("%s does not say whom to ask", name)
		}
		if !bytes.Contains(page, []byte("{{copyright}}")) {
			t.Errorf("%s does not carry the copyright line", name)
		}
	}
}

func TestAboutPagesHoldNoScriptOrStyleOfTheirOwnAndNoOtherSite(t *testing.T) {
	for _, name := range aboutPages {
		page := string(read(t, name+".html"))
		for _, bad := range []string{"<style", " style=", " onclick=", "<iframe"} {
			if strings.Contains(page, bad) {
				t.Errorf("%s contains %q, which the page's security policy would refuse", name, bad)
			}
		}
		for _, inline := range regexp.MustCompile(`(?s)<script\b[^>]*>`).FindAllString(page, -1) {
			if !strings.Contains(inline, " src=") {
				t.Errorf("%s has an inline script", name)
			}
		}
		for _, src := range regexp.MustCompile(`(?:src|href)="(https?:)?//[^"]*"`).FindAllString(page, -1) {
			if !strings.Contains(src, "href=") {
				t.Errorf("%s loads %s from another site", name, src)
			}
		}
	}
}

func TestLoginMenuAndHelpLinkToTheAboutPages(t *testing.T) {
	page := read(t, "index.html")
	login := regexp.MustCompile(`(?s)<section id="login".*?</section>`).Find(page)
	help := regexp.MustCompile(`(?s)<dialog id="help".*?</dialog>`).Find(page)
	menu := regexp.MustCompile(`(?s)<h2 class="more-title">Link and help</h2>.*?<h2 class="more-title">Logged in as</h2>`).Find(page)
	for where, part := range map[string][]byte{"login": login, "help": help, "menu": menu} {
		if len(part) == 0 {
			t.Fatalf("the %s was not found", where)
		}
		for _, name := range aboutPages {
			if !bytes.Contains(part, []byte(`href="`+name+`"`)) {
				t.Errorf("the %s does not link to %s", where, name)
			}
		}
	}
}
