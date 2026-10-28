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

// Default is the table built for the newest server version checked in.
func Default() (*Table, error) {
	defaultOnce.Do(func() {
		names, err := Versions()
		if err != nil || len(names) == 0 {
			defaultErr = fmt.Errorf("place: no embedded table: %v", err)
			return
		}
		defaultTable, defaultErr = Load(names[len(names)-1])
	})
	return defaultTable, defaultErr
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
