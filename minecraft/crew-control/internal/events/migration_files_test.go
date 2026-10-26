package events

import (
	"os"
	"strings"
	"testing"
)

// Every migration after V11 ships the same way: the ConfigMap entry is the SQL
// file indented under its key, nothing else.
func TestLaterConfigMapEntriesAreTheirMigrationsIndented(t *testing.T) {
	for _, name := range []string{"V12__minecraft_crew_event_analytics", "V13__minecraft_crew_alerts", "V14__minecraft_crew_position"} {
		sql, err := os.ReadFile("../../migrations/" + name + ".sql")
		if err != nil {
			t.Fatal(err)
		}
		entry, err := os.ReadFile("../../migrations/" + name + ".configmap.yaml")
		if err != nil {
			t.Fatal(err)
		}
		want := []string{"  " + name + ".sql: |"}
		for _, l := range strings.Split(strings.TrimRight(string(sql), "\n"), "\n") {
			if l == "" {
				want = append(want, "")
			} else {
				want = append(want, "    "+l)
			}
		}
		if got := strings.TrimRight(string(entry), "\n"); got != strings.Join(want, "\n") {
			t.Errorf("%s: the ConfigMap entry has drifted from the SQL file", name)
		}
	}
}

// Forward-only: a shipped migration may add, never remove or rewrite.
func TestMigrationsOnlyAdd(t *testing.T) {
	for _, name := range []string{"V11__minecraft_crew_events", "V12__minecraft_crew_event_analytics", "V13__minecraft_crew_alerts", "V14__minecraft_crew_position"} {
		b, err := os.ReadFile("../../migrations/" + name + ".sql")
		if err != nil {
			t.Fatal(err)
		}
		var code []string
		for _, l := range strings.Split(string(b), "\n") {
			if !strings.HasPrefix(strings.TrimSpace(l), "--") {
				code = append(code, l)
			}
		}
		sql := strings.ToUpper(strings.Join(code, "\n"))
		for _, bad := range []string{"DROP ", "TRUNCATE", "DELETE ", "UPDATE ", "RENAME", "ALTER COLUMN", "SET NOT NULL"} {
			if strings.Contains(sql, bad) {
				t.Errorf("%s contains %q: migrations only add", name, strings.TrimSpace(bad))
			}
		}
	}
}
