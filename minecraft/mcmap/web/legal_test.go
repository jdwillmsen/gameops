package web

import (
	"bytes"
	"regexp"
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
