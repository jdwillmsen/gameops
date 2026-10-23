package events

import (
	"os"
	"strings"
	"testing"
)

const (
	migrationFile = "../../migrations/V11__minecraft_crew_events.sql"
	configMapFile = "../../migrations/V11__minecraft_crew_events.configmap.yaml"
)

// The platform repo ships the migration as a ConfigMap entry, not as a file.
// The entry must be the SQL and nothing else, indented under its key, so what
// reviewers approve there is what the DB tests run here.
func TestConfigMapEntryIsTheMigrationIndented(t *testing.T) {
	sql, err := os.ReadFile(migrationFile)
	if err != nil {
		t.Fatal(err)
	}
	entry, err := os.ReadFile(configMapFile)
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimRight(string(sql), "\n"), "\n")
	want := []string{"  V11__minecraft_crew_events.sql: |"}
	for _, l := range lines {
		if l == "" {
			want = append(want, "")
		} else {
			want = append(want, "    "+l)
		}
	}
	if got := strings.TrimRight(string(entry), "\n"); got != strings.Join(want, "\n") {
		t.Fatal("the ConfigMap entry has drifted from the SQL file; regenerate it from the SQL")
	}
}

// What the code relies on: the table, its CHECK on severity and the four
// indexes the query shapes need, each ending in the keyset pair.
func TestMigrationDeclaresWhatTheStoreAssumes(t *testing.T) {
	b, err := os.ReadFile(migrationFile)
	if err != nil {
		t.Fatal(err)
	}
	sql := string(b)
	for _, want := range []string{
		"CREATE TABLE IF NOT EXISTS minecraft.crew_event",
		"CHECK (severity IN ('ok', 'info', 'warn', 'crit'))",
		"ON minecraft.crew_event (unit, at, id)",
		"ON minecraft.crew_event (kind, at, id)",
		"ON minecraft.crew_event (severity, at, id)",
		"ON minecraft.crew_event (at, id)",
	} {
		if !strings.Contains(sql, want) {
			t.Errorf("migration lacks %q", want)
		}
	}
	// Idempotent, like V9 and V10: Flyway never replays it, but a manual
	// re-apply on a half-migrated database must not fail.
	if strings.Count(sql, "IF NOT EXISTS") != 5 {
		t.Error("every statement must be IF NOT EXISTS")
	}
}
