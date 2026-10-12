package config

import (
	"path/filepath"
	"testing"
)

// It writes to the volume every day for good, so an image update must not
// switch it on by itself.
func TestLoad_HistoryIsOffUntilAskedFor(t *testing.T) {
	c, err := Load(with())
	if err != nil {
		t.Fatal(err)
	}
	if c.History {
		t.Error("history is on by default")
	}
}

func TestLoad_HistoryIsKeptUnderTheDataDirUnlessGivenAPlaceOfItsOwn(t *testing.T) {
	c, err := Load(with("HISTORY_ENABLED", "true", "DATA_DIR", "/var/map"))
	if err != nil {
		t.Fatal(err)
	}
	if !c.History || c.HistoryDir != filepath.Join("/var/map", "history") {
		t.Errorf("history = %v in %q", c.History, c.HistoryDir)
	}
	c, err = Load(with("HISTORY_ENABLED", "true", "HISTORY_DIR", "/history"))
	if err != nil {
		t.Fatal(err)
	}
	if c.HistoryDir != "/history" {
		t.Errorf("history dir = %q", c.HistoryDir)
	}
}

func TestLoad_HistoryRejects(t *testing.T) {
	for name, pairs := range map[string][]string{
		"a switch that is not one":     {"HISTORY_ENABLED", "yes please"},
		"a directory that is relative": {"HISTORY_ENABLED", "true", "HISTORY_DIR", "history"},
	} {
		if _, err := Load(with(pairs...)); err == nil {
			t.Errorf("%s: want an error", name)
		}
	}
}
