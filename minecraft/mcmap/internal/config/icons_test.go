package config

import (
	"strings"
	"testing"
)

func TestLoad_Icons(t *testing.T) {
	c, err := Load(with())
	if err != nil {
		t.Fatal(err)
	}
	if !c.Icons || c.IconsRef != DefaultIconsRef {
		t.Errorf("icon defaults = %v at %q", c.Icons, c.IconsRef)
	}
	// The default is a commit: a tag can be moved, and then what is fetched
	// would change with nothing here having changed.
	if len(DefaultIconsRef) != 40 || strings.Trim(DefaultIconsRef, "0123456789abcdef") != "" {
		t.Errorf("the default pin %q is not a commit", DefaultIconsRef)
	}

	c, err = Load(with("ICONS_ENABLED", "false", "ICONS_REF", "v1.26.50.4"))
	if err != nil {
		t.Fatal(err)
	}
	if c.Icons || c.IconsRef != "v1.26.50.4" {
		t.Errorf("icons = %v at %q", c.Icons, c.IconsRef)
	}

	if _, err := Load(with("ICONS_ENABLED", "sometimes")); err == nil || !strings.Contains(err.Error(), "ICONS_ENABLED") {
		t.Errorf("ICONS_ENABLED=sometimes: %v", err)
	}
	// The pin becomes part of an address on another host.
	for _, ref := range []string{"../../other/repo", "main/../../x", "v1 26", "a?b=c", "x#y", "-flag", strings.Repeat("a", 101)} {
		if _, err := Load(with("ICONS_REF", ref)); err == nil || !strings.Contains(err.Error(), "ICONS_REF") {
			t.Errorf("ICONS_REF=%q: %v", ref, err)
		}
	}
}
