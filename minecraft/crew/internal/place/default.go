package place

import (
	"embed"
	"fmt"
	"sort"
	"strings"
	"sync"
)

//go:embed table/*.json.gz
var tables embed.FS

var (
	defaultOnce  sync.Once
	defaultTable *Table
	defaultErr   error
)

// loadDefault builds the default table; tests replace it to inject a bad
// embedded blob.
var loadDefault = func() (*Table, error) {
	names, err := Versions()
	if err != nil || len(names) == 0 {
		return nil, fmt.Errorf("place: no embedded table: %v", err)
	}
	return Load(names[len(names)-1])
}

// Default is the table built for the newest server version checked in. When
// it cannot be loaded the table is empty, never nil, and the error says why;
// Err reports the same error to callers of the package-level helpers.
func Default() (*Table, error) {
	defaultOnce.Do(func() {
		defaultTable, defaultErr = loadDefault()
		if defaultErr != nil || defaultTable == nil {
			defaultTable = &Table{}
		}
	})
	return defaultTable, defaultErr
}

// Err is the error loading the embedded default table, nil when it loaded.
// Lookup and Derived answer as if the block were unknown when it did not, so
// a caller that must tell a broken table from an unreachable state checks Err.
func Err() error {
	_, err := Default()
	return err
}

// Versions lists the server versions an embedded table exists for, oldest
// first by version number.
func Versions() ([]string, error) {
	entries, err := tables.ReadDir("table")
	if err != nil {
		return nil, err
	}
	var out []string
	for _, e := range entries {
		if v, ok := strings.CutSuffix(e.Name(), ".json.gz"); ok {
			out = append(out, v)
		}
	}
	sort.Slice(out, func(i, j int) bool { return versionLess(out[i], out[j]) })
	return out, nil
}

// Load reads the embedded table for a server version.
func Load(version string) (*Table, error) {
	f, err := tables.Open("table/" + version + ".json.gz")
	if err != nil {
		return nil, fmt.Errorf("place: no table for server version %q: %w", version, err)
	}
	defer f.Close()
	return Read(f)
}

func versionLess(a, b string) bool {
	pa, pb := strings.Split(a, "."), strings.Split(b, ".")
	for i := 0; i < len(pa) && i < len(pb); i++ {
		var x, y int
		fmt.Sscanf(pa[i], "%d", &x)
		fmt.Sscanf(pb[i], "%d", &y)
		if x != y {
			return x < y
		}
	}
	return len(pa) < len(pb)
}

// Lookup is Table.Lookup on the embedded default table. It reports false when
// no table is embedded or the block or state is not in it.
func Lookup(block string, want map[string]any) (Recipe, bool) {
	t, err := Default()
	if err != nil {
		return Recipe{}, false
	}
	return t.Lookup(block, want)
}

// Derived is Table.Derived on the embedded default table.
func Derived(block string) []string {
	t, err := Default()
	if err != nil {
		return nil
	}
	return t.Derived(block)
}
