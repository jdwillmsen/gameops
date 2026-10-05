package pack

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	files "github.com/jdwillmsen/gameops/minecraft/mcmap/pack"
)

const level = "FWB"

func quiet() *slog.Logger { return slog.New(slog.NewTextHandler(io.Discard, nil)) }

func logged() (*slog.Logger, *bytes.Buffer) {
	var out bytes.Buffer
	return slog.New(slog.NewTextHandler(&out, nil)), &out
}

// server lays out a data directory the way a server that has run once
// leaves it: a world, and the directory packs are installed into.
func server(t *testing.T) string {
	t.Helper()
	dataDir := t.TempDir()
	for _, dir := range []string{filepath.Join("worlds", level), "behavior_packs"} {
		if err := os.MkdirAll(filepath.Join(dataDir, dir), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	return dataDir
}

func env(dataDir string, extra ...string) func(string) string {
	vars := map[string]string{"DATA_DIR": dataDir, "LEVEL_NAME": level}
	for i := 0; i+1 < len(extra); i += 2 {
		vars[extra[i]] = extra[i+1]
	}
	return func(name string) string { return vars[name] }
}

func registryPath(dataDir string) string {
	return filepath.Join(dataDir, "worlds", level, registryName)
}

func packPath(dataDir string, name string) string {
	return filepath.Join(dataDir, "behavior_packs", DirName, filepath.FromSlash(name))
}

func read(t *testing.T, path string) string {
	t.Helper()
	content, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(content)
}

func registry(t *testing.T, dataDir string) []map[string]any {
	t.Helper()
	var entries []map[string]any
	if err := json.Unmarshal([]byte(read(t, registryPath(dataDir))), &entries); err != nil {
		t.Fatalf("the world's pack list is not valid JSON: %v", err)
	}
	return entries
}

func ourID(t *testing.T) string {
	t.Helper()
	meta, err := readManifest()
	if err != nil {
		t.Fatal(err)
	}
	return meta.Header.UUID
}

// tree is every path under dir with its content, so two of them compare
// equal only when nothing at all was added, removed or changed.
func tree(t *testing.T, dir string) map[string]string {
	t.Helper()
	found := map[string]string{}
	err := filepath.WalkDir(dir, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(dir, path)
		if d.IsDir() {
			found[rel+"/"] = ""
			return nil
		}
		content, err := os.ReadFile(path)
		found[rel] = string(content)
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	return found
}

func sameTree(t *testing.T, before, after map[string]string) {
	t.Helper()
	for path, content := range after {
		if was, ok := before[path]; !ok {
			t.Errorf("%s was created", path)
		} else if was != content {
			t.Errorf("%s was changed", path)
		}
	}
	for path := range before {
		if _, ok := after[path]; !ok {
			t.Errorf("%s was removed", path)
		}
	}
}

// stub is an installer whose renames and writes go through the given
// functions; nil means the real one.
func stub(dataDir string, mobCap int, rename func(from, to string) error, write func(path string, content []byte) error) *installer {
	in := &installer{dataDir: dataDir, level: level, mobCap: mobCap, logger: quiet(), rename: os.Rename, write: writeSynced}
	if rename != nil {
		in.rename = rename
	}
	if write != nil {
		in.write = write
	}
	return in
}

// tornWrite leaves half the content behind and then fails, which is what a
// full disk does to a write.
func tornWrite(matches func(path string) bool, failure error) func(string, []byte) error {
	return func(path string, content []byte) error {
		if !matches(path) {
			return writeSynced(path, content)
		}
		_ = os.WriteFile(path, content[:len(content)/2], 0o644)
		return failure
	}
}

func skipAsRoot(t *testing.T) {
	t.Helper()
	if os.Geteuid() == 0 {
		t.Skip("root ignores directory permissions")
	}
}

func TestInstallWritesPackAndRegistersIt(t *testing.T) {
	dataDir := server(t)
	if status := Run("install-pack", env(dataDir), quiet()); status != 0 {
		t.Fatalf("exit status %d", status)
	}

	for _, name := range []string{"manifest.json", "scripts/main.js"} {
		want, _ := files.FS.ReadFile(name)
		if got := read(t, packPath(dataDir, name)); got != string(want) {
			t.Errorf("%s differs from the embedded file", name)
		}
	}
	config := read(t, packPath(dataDir, configName))
	if !strings.Contains(config, "export const MOB_CAP = 1000;") || !strings.Contains(config, Version(DefaultMobCap)) {
		t.Errorf("config.js = %q", config)
	}
	entries := registry(t, dataDir)
	if len(entries) != 1 || entries[0]["pack_id"] != ourID(t) {
		t.Fatalf("registry = %v", entries)
	}
	if v, _ := json.Marshal(entries[0]["version"]); string(v) != "[1,0,0]" {
		t.Errorf("registered version = %s", v)
	}
	if _, err := os.Stat(filepath.Join(dataDir, stagingName)); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("staging was left behind: %v", err)
	}
	for _, path := range []string{packPath(dataDir, "scripts/main.js"), registryPath(dataDir)} {
		info, err := os.Stat(path)
		if err != nil {
			t.Fatal(err)
		}
		// The server may run as another member of the volume's group.
		if info.Mode().Perm()&0o044 != 0o044 {
			t.Errorf("%s is %v, not readable by the server", path, info.Mode().Perm())
		}
	}
}

func TestInstallIsIdempotent(t *testing.T) {
	dataDir := server(t)
	if err := Install(dataDir, level, DefaultMobCap, quiet()); err != nil {
		t.Fatal(err)
	}
	before := tree(t, dataDir)

	// A second run that renamed or wrote anything would have changed something.
	in := stub(dataDir, DefaultMobCap, func(from, to string) error {
		t.Errorf("an installed pack was rewritten: rename %s -> %s", from, to)
		return os.Rename(from, to)
	}, func(path string, content []byte) error {
		t.Errorf("an installed pack was rewritten: write %s", path)
		return writeSynced(path, content)
	})
	if err := in.install(); err != nil {
		t.Fatal(err)
	}
	sameTree(t, before, tree(t, dataDir))
}

func TestInstallRepairsAMissingRegistration(t *testing.T) {
	dataDir := server(t)
	if err := Install(dataDir, level, DefaultMobCap, quiet()); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(registryPath(dataDir)); err != nil {
		t.Fatal(err)
	}
	if err := Install(dataDir, level, DefaultMobCap, quiet()); err != nil {
		t.Fatal(err)
	}
	if entries := registry(t, dataDir); len(entries) != 1 || entries[0]["pack_id"] != ourID(t) {
		t.Fatalf("registry = %v", entries)
	}
}

func TestInstallRepairsATamperedPack(t *testing.T) {
	for name, tamper := range map[string]func(t *testing.T, dataDir string){
		"changed file": func(t *testing.T, dataDir string) {
			if err := os.WriteFile(packPath(dataDir, "scripts/main.js"), []byte("// truncated"), 0o644); err != nil {
				t.Fatal(err)
			}
		},
		"missing file": func(t *testing.T, dataDir string) {
			if err := os.Remove(packPath(dataDir, configName)); err != nil {
				t.Fatal(err)
			}
		},
		// The server loads whatever a pack directory holds, so a file this
		// did not put there is not left there.
		"stray file": func(t *testing.T, dataDir string) {
			if err := os.WriteFile(packPath(dataDir, "scripts/extra.js"), []byte("// stray"), 0o644); err != nil {
				t.Fatal(err)
			}
		},
	} {
		t.Run(name, func(t *testing.T) {
			dataDir := server(t)
			if err := Install(dataDir, level, DefaultMobCap, quiet()); err != nil {
				t.Fatal(err)
			}
			whole := tree(t, dataDir)
			tamper(t, dataDir)
			if err := Install(dataDir, level, DefaultMobCap, quiet()); err != nil {
				t.Fatal(err)
			}
			sameTree(t, whole, tree(t, dataDir))
		})
	}
}

func TestInstallPreservesOtherPacks(t *testing.T) {
	dataDir := server(t)
	// Tabs, spaced colons and a field this code has never heard of: the
	// other pack's entry has to come through with all of it.
	other := "[\n\t{\n\t\t\"pack_id\" : \"11111111-2222-3333-4444-555555555555\",\n\t\t\"version\" : [ 2, 5, 1 ],\n\t\t\"subpack\" : \"hd\"\n\t}\n]\n"
	if err := os.WriteFile(registryPath(dataDir), []byte(other), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := Install(dataDir, level, DefaultMobCap, quiet()); err != nil {
		t.Fatal(err)
	}

	entries := registry(t, dataDir)
	if len(entries) != 2 {
		t.Fatalf("registry = %v", entries)
	}
	first, _ := json.Marshal(entries[0])
	if string(first) != `{"pack_id":"11111111-2222-3333-4444-555555555555","subpack":"hd","version":[2,5,1]}` {
		t.Errorf("the other pack's entry was altered: %s", first)
	}
	if entries[1]["pack_id"] != ourID(t) {
		t.Errorf("this pack was not appended: %v", entries[1])
	}

	if err := Uninstall(dataDir, level, quiet()); err != nil {
		t.Fatal(err)
	}
	if entries := registry(t, dataDir); len(entries) != 1 || entries[0]["subpack"] != "hd" {
		t.Errorf("uninstall did not leave exactly the other pack: %v", entries)
	}
}

func TestInstallReplacesAStaleRegistration(t *testing.T) {
	dataDir := server(t)
	stale := `[{"pack_id":"` + ourID(t) + `","version":[0,9,0]},{"pack_id":"` + ourID(t) + `","version":[0,8,0]}]`
	if err := os.WriteFile(registryPath(dataDir), []byte(stale), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := Install(dataDir, level, DefaultMobCap, quiet()); err != nil {
		t.Fatal(err)
	}
	entries := registry(t, dataDir)
	if len(entries) != 1 {
		t.Fatalf("registry = %v", entries)
	}
	if v, _ := json.Marshal(entries[0]["version"]); string(v) != "[1,0,0]" {
		t.Errorf("registered version = %s", v)
	}
}

func TestInstallExitsZeroWhenTheWorldIsMissing(t *testing.T) {
	for name, prepare := range map[string]func(t *testing.T) string{
		"no data directory": func(t *testing.T) string { return filepath.Join(t.TempDir(), "absent") },
		"no world yet": func(t *testing.T) string {
			dataDir := t.TempDir()
			if err := os.MkdirAll(filepath.Join(dataDir, "behavior_packs"), 0o755); err != nil {
				t.Fatal(err)
			}
			return dataDir
		},
		"world is a file": func(t *testing.T) string {
			dataDir := t.TempDir()
			if err := os.MkdirAll(filepath.Join(dataDir, "worlds"), 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(dataDir, "worlds", level), nil, 0o644); err != nil {
				t.Fatal(err)
			}
			return dataDir
		},
	} {
		t.Run(name, func(t *testing.T) {
			dataDir := prepare(t)
			parent := filepath.Dir(dataDir)
			before := tree(t, parent)
			logger, out := logged()
			if status := Run("install-pack", env(dataDir), logger); status != 0 {
				t.Fatalf("exit status %d would keep the server from starting", status)
			}
			if !strings.Contains(out.String(), "world directory") {
				t.Errorf("the reason was not logged: %s", out)
			}
			// Nothing may be created for a world the server has not made.
			sameTree(t, before, tree(t, parent))
		})
	}
}

func TestInstallExitsZeroWithoutItsEnvironment(t *testing.T) {
	dataDir := server(t)
	for name, getenv := range map[string]func(string) string{
		"nothing set":       func(string) string { return "" },
		"no level":          func(n string) string { return map[string]string{"DATA_DIR": dataDir}[n] },
		"level with a path": func(n string) string { return map[string]string{"DATA_DIR": dataDir, "LEVEL_NAME": "../" + level}[n] },
		"level is dot-dot":  func(n string) string { return map[string]string{"DATA_DIR": dataDir, "LEVEL_NAME": ".."}[n] },
	} {
		t.Run(name, func(t *testing.T) {
			before := tree(t, dataDir)
			logger, out := logged()
			if status := Run("install-pack", getenv, logger); status != 0 {
				t.Fatalf("exit status %d would keep the server from starting", status)
			}
			if out.Len() == 0 {
				t.Error("nothing was logged")
			}
			sameTree(t, before, tree(t, dataDir))
		})
	}
}

func TestInstallExitsZeroOnAnUnwritableDataDir(t *testing.T) {
	skipAsRoot(t)
	for name, lock := range map[string][]string{
		"data directory":  {"."},
		"pack directory":  {"behavior_packs"},
		"world directory": {filepath.Join("worlds", level)},
		"everything":      {".", "behavior_packs", "worlds", filepath.Join("worlds", level)},
	} {
		t.Run(name, func(t *testing.T) {
			dataDir := server(t)
			for _, dir := range lock {
				path := filepath.Join(dataDir, dir)
				if err := os.Chmod(path, 0o555); err != nil {
					t.Fatal(err)
				}
				t.Cleanup(func() { _ = os.Chmod(path, 0o755) })
			}
			logger, out := logged()
			if status := Run("install-pack", env(dataDir), logger); status != 0 {
				t.Fatalf("exit status %d would keep the server from starting", status)
			}
			if !strings.Contains(out.String(), "permission denied") {
				t.Errorf("the reason was not logged: %s", out)
			}
			if _, err := os.Stat(registryPath(dataDir)); !errors.Is(err, os.ErrNotExist) {
				t.Errorf("a pack that could not be installed was registered: %v", err)
			}
		})
	}
}

func TestInstallExitsZeroOnUnparseableWorldPacks(t *testing.T) {
	for name, content := range map[string]string{
		"truncated":     `[{"pack_id":"11111111-2222`,
		"empty":         ``,
		"not a list":    `{"pack_id":"11111111-2222-3333-4444-555555555555"}`,
		"list of junk":  `[1, "two"]`,
		"binary":        "\x00\x01\x02",
		"wrong id type": `[{"pack_id": 7}]`,
	} {
		t.Run(name, func(t *testing.T) {
			dataDir := server(t)
			if err := os.WriteFile(registryPath(dataDir), []byte(content), 0o644); err != nil {
				t.Fatal(err)
			}
			before := tree(t, dataDir)
			logger, out := logged()
			if status := Run("install-pack", env(dataDir), logger); status != 0 {
				t.Fatalf("exit status %d would keep the server from starting", status)
			}
			if !strings.Contains(out.String(), registryName) {
				t.Errorf("the reason was not logged: %s", out)
			}
			// A list this cannot read is not this code's to overwrite, and
			// a pack with no registration is not worth writing.
			sameTree(t, before, tree(t, dataDir))

			if status := Run("uninstall-pack", env(dataDir), quiet()); status != 0 {
				t.Fatalf("uninstall exit status %d", status)
			}
			sameTree(t, before, tree(t, dataDir))
		})
	}
}

func TestInstallIsAtomicUnderAFailedWrite(t *testing.T) {
	failing := errors.New("no space left on device")
	other := `[{"pack_id":"11111111-2222-3333-4444-555555555555","version":[2,5,1]}]`

	t.Run("registration torn mid-write", func(t *testing.T) {
		dataDir := server(t)
		if err := os.WriteFile(registryPath(dataDir), []byte(other), 0o644); err != nil {
			t.Fatal(err)
		}
		inWorld := func(path string) bool { return filepath.Dir(path) == filepath.Dir(registryPath(dataDir)) }
		err := stub(dataDir, DefaultMobCap, nil, tornWrite(inWorld, failing)).install()
		if !errors.Is(err, failing) || errors.Is(err, ErrHalfWritten) {
			t.Fatalf("err = %v", err)
		}
		if got := read(t, registryPath(dataDir)); got != other {
			t.Errorf("the registry was changed by a write that failed: %q", got)
		}
		if left, _ := filepath.Glob(filepath.Join(dataDir, "worlds", level, "*")); len(left) != 1 {
			t.Errorf("a temporary file was left in the world: %v", left)
		}
	})

	t.Run("registration not moved into place", func(t *testing.T) {
		dataDir := server(t)
		if err := os.WriteFile(registryPath(dataDir), []byte(other), 0o644); err != nil {
			t.Fatal(err)
		}
		err := stub(dataDir, DefaultMobCap, func(from, to string) error {
			if to == registryPath(dataDir) {
				return failing
			}
			return os.Rename(from, to)
		}, nil).install()
		if !errors.Is(err, failing) || errors.Is(err, ErrHalfWritten) {
			t.Fatalf("err = %v", err)
		}
		if got := read(t, registryPath(dataDir)); got != other {
			t.Errorf("the registry was changed by a write that failed: %q", got)
		}
		if left, _ := filepath.Glob(filepath.Join(dataDir, "worlds", level, "*")); len(left) != 1 {
			t.Errorf("a temporary file was left in the world: %v", left)
		}
	})

	t.Run("pack torn mid-write", func(t *testing.T) {
		for _, name := range []string{"manifest.json", "scripts/main.js", configName} {
			dataDir := server(t)
			if err := Install(dataDir, level, 400, quiet()); err != nil {
				t.Fatal(err)
			}
			before := tree(t, dataDir)
			isFile := func(path string) bool { return strings.HasSuffix(filepath.ToSlash(path), "/"+name) }
			err := stub(dataDir, 800, nil, tornWrite(isFile, failing)).install()
			if !errors.Is(err, failing) || errors.Is(err, ErrHalfWritten) {
				t.Fatalf("%s: err = %v", name, err)
			}
			// The pack that was there is whole, with nothing of the new
			// one mixed in and nothing left lying beside it.
			sameTree(t, before, tree(t, dataDir))
		}
	})

	t.Run("pack not moved into place", func(t *testing.T) {
		dataDir := server(t)
		if err := Install(dataDir, level, 400, quiet()); err != nil {
			t.Fatal(err)
		}
		before := tree(t, dataDir)
		err := stub(dataDir, 800, func(from, to string) error {
			if to == packPath(dataDir, "") && strings.HasSuffix(from, "new") {
				return failing
			}
			return os.Rename(from, to)
		}, nil).install()
		if !errors.Is(err, failing) || errors.Is(err, ErrHalfWritten) {
			t.Fatalf("err = %v", err)
		}
		sameTree(t, before, tree(t, dataDir))
	})

	t.Run("first install torn mid-write", func(t *testing.T) {
		dataDir := server(t)
		before := tree(t, dataDir)
		isScript := func(path string) bool { return strings.HasSuffix(path, "main.js") }
		err := stub(dataDir, DefaultMobCap, nil, tornWrite(isScript, failing)).install()
		if !errors.Is(err, failing) {
			t.Fatalf("err = %v", err)
		}
		// Half a pack must never be where the server looks for one, and
		// must never be registered.
		sameTree(t, before, tree(t, dataDir))
	})
}

func TestInstallExitsNonZeroOnlyWhenItCannotRollBack(t *testing.T) {
	dataDir := server(t)
	if err := Install(dataDir, level, 400, quiet()); err != nil {
		t.Fatal(err)
	}
	moved := false
	in := stub(dataDir, 800, func(from, to string) error {
		if !moved {
			moved = true
			return os.Rename(from, to)
		}
		return errors.New("input/output error")
	}, nil)
	err := in.install()
	if !errors.Is(err, ErrHalfWritten) {
		t.Fatalf("err = %v, want ErrHalfWritten", err)
	}
	if status := exitStatus("install-pack", err, quiet()); status != 1 {
		t.Errorf("exit status %d for a half-written install, want 1", status)
	}
	// The old pack is still on the volume for whoever has to look.
	if got := read(t, filepath.Join(dataDir, stagingName, "old", filepath.FromSlash(configName))); !strings.Contains(got, "MOB_CAP = 400") {
		t.Errorf("the pack that was moved aside is gone: %q", got)
	}

	// The retry the failure bought repairs it, and cannot fail the same way.
	logger, out := logged()
	if status := Run("install-pack", env(dataDir, "PACK_MOB_CAP", "800"), logger); status != 0 {
		t.Fatalf("exit status %d on the retry: %s", status, out)
	}
	if got := read(t, packPath(dataDir, configName)); !strings.Contains(got, "MOB_CAP = 800") {
		t.Errorf("config.js = %q", got)
	}
}

func TestRunReportsOnlyAHalfWrittenInstallAsAFailure(t *testing.T) {
	dataDir := server(t)
	if err := Install(dataDir, level, 400, quiet()); err != nil {
		t.Fatal(err)
	}
	// The installed pack cannot be moved aside, so nothing has changed
	// when it fails: an ordinary failure, and status zero.
	skipAsRoot(t)
	if err := os.Chmod(filepath.Join(dataDir, "behavior_packs"), 0o555); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(filepath.Join(dataDir, "behavior_packs"), 0o755) })
	before := tree(t, filepath.Join(dataDir, "behavior_packs"))
	if status := Run("install-pack", env(dataDir, "PACK_MOB_CAP", "800"), quiet()); status != 0 {
		t.Fatalf("exit status %d would keep the server from starting", status)
	}
	sameTree(t, before, tree(t, filepath.Join(dataDir, "behavior_packs")))
}

func TestRunSurvivesAPanic(t *testing.T) {
	logger, out := logged()
	status := Run("install-pack", func(string) string { panic("getenv exploded") }, logger)
	if status != 0 {
		t.Fatalf("exit status %d would keep the server from starting", status)
	}
	if !strings.Contains(out.String(), "getenv exploded") {
		t.Errorf("the panic was not logged: %s", out)
	}
}

func TestInstallFallsBackOnABadMobCap(t *testing.T) {
	for raw, want := range map[string]int{
		"":                     DefaultMobCap,
		"  ":                   DefaultMobCap,
		"250":                  250,
		" 250 ":                250,
		"0":                    DefaultMobCap,
		"-5":                   DefaultMobCap,
		"lots":                 DefaultMobCap,
		"1e3":                  DefaultMobCap,
		"12.5":                 DefaultMobCap,
		"5000":                 MaxMobCap,
		"5001":                 MaxMobCap,
		"9999999":              MaxMobCap,
		"99999999999999999999": DefaultMobCap,
	} {
		t.Run(raw, func(t *testing.T) {
			dataDir := server(t)
			logger, out := logged()
			if status := Run("install-pack", env(dataDir, "PACK_MOB_CAP", raw), logger); status != 0 {
				t.Fatalf("exit status %d", status)
			}
			config := read(t, packPath(dataDir, configName))
			if !strings.Contains(config, "export const MOB_CAP = "+itoa(want)+";") {
				t.Errorf("config.js = %q, want cap %d", config, want)
			}
			fellBack := strings.Contains(out.String(), "PACK_MOB_CAP")
			if wantLog := strings.TrimSpace(raw) != "" && itoa(want) != strings.TrimSpace(raw); fellBack != wantLog {
				t.Errorf("fallback logged = %v, want %v: %s", fellBack, wantLog, out)
			}
		})
	}
}

func TestInstallRewritesConfigWhenTheCapChanges(t *testing.T) {
	dataDir := server(t)
	if status := Run("install-pack", env(dataDir, "PACK_MOB_CAP", "1000"), quiet()); status != 0 {
		t.Fatal(status)
	}
	first := read(t, packPath(dataDir, configName))
	if status := Run("install-pack", env(dataDir, "PACK_MOB_CAP", "300"), quiet()); status != 0 {
		t.Fatal(status)
	}
	second := read(t, packPath(dataDir, configName))

	if !strings.Contains(second, "export const MOB_CAP = 300;") {
		t.Errorf("config.js = %q", second)
	}
	if Version(1000) == Version(300) || !strings.Contains(first, Version(1000)) || !strings.Contains(second, Version(300)) {
		t.Errorf("the version does not follow the cap: %q then %q", first, second)
	}
	if entries := registry(t, dataDir); len(entries) != 1 {
		t.Errorf("the pack was registered twice: %v", entries)
	}
}

func TestUninstallRemovesThePackAndItsRegistration(t *testing.T) {
	dataDir := server(t)
	clean := tree(t, dataDir)
	if status := Run("install-pack", env(dataDir), quiet()); status != 0 {
		t.Fatal(status)
	}
	if status := Run("uninstall-pack", env(dataDir), quiet()); status != 0 {
		t.Fatal(status)
	}
	if _, err := os.Stat(packPath(dataDir, "")); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("the pack is still on disk: %v", err)
	}
	if entries := registry(t, dataDir); len(entries) != 0 {
		t.Errorf("registry = %v", entries)
	}
	if got := read(t, registryPath(dataDir)); strings.TrimSpace(got) != "[]" {
		t.Errorf("an emptied registry must still be a list: %q", got)
	}

	// Uninstalling what is not installed changes nothing and fails nothing.
	if err := os.Remove(registryPath(dataDir)); err != nil {
		t.Fatal(err)
	}
	if status := Run("uninstall-pack", env(dataDir), quiet()); status != 0 {
		t.Fatal(status)
	}
	sameTree(t, clean, tree(t, dataDir))
}

func TestUninstallKeepsThePackWhenItCannotUnregisterIt(t *testing.T) {
	dataDir := server(t)
	if err := Install(dataDir, level, DefaultMobCap, quiet()); err != nil {
		t.Fatal(err)
	}
	before := tree(t, dataDir)
	in := stub(dataDir, 0, func(string, string) error {
		return errors.New("read-only file system")
	}, nil)
	if err := in.uninstall(); err == nil || errors.Is(err, ErrHalfWritten) {
		t.Fatalf("err = %v", err)
	}
	// Still registered, so the files it names have to still be there.
	sameTree(t, before, tree(t, dataDir))
}

// The world this installs into is no-cheats survival with no experiments,
// and stays that way only while the pack asks for nothing but the stable
// scripting module.
func TestManifestAsksForTheStableAPIAndNothingElse(t *testing.T) {
	raw, err := files.FS.ReadFile("manifest.json")
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]json.RawMessage
	if err := json.Unmarshal(raw, &m); err != nil {
		t.Fatal(err)
	}
	for key := range m {
		switch key {
		case "format_version", "header", "modules", "dependencies":
		default:
			t.Errorf("manifest has %q: capabilities, metadata and subpacks are all out of bounds", key)
		}
	}
	var deps []map[string]any
	if err := json.Unmarshal(m["dependencies"], &deps); err != nil {
		t.Fatal(err)
	}
	if len(deps) != 1 || deps[0]["module_name"] != "@minecraft/server" || deps[0]["version"] != "2.10.0" || len(deps[0]) != 2 {
		t.Errorf("dependencies = %v, want exactly @minecraft/server 2.10.0", deps)
	}
	var modules []map[string]any
	if err := json.Unmarshal(m["modules"], &modules); err != nil {
		t.Fatal(err)
	}
	if len(modules) != 1 || modules[0]["type"] != "script" || modules[0]["entry"] != "scripts/main.js" {
		t.Errorf("modules = %v, want one script module", modules)
	}
	if lower := strings.ToLower(string(raw)); strings.Contains(lower, "beta") || strings.Contains(lower, "experimental") || strings.Contains(lower, "script_eval") {
		t.Error("manifest mentions a beta, experimental or eval feature")
	}
	// A changed id orphans the pack in every world it is registered with.
	meta, err := readManifest()
	if err != nil {
		t.Fatal(err)
	}
	if meta.Header.UUID != "dc4810d2-bfe2-4f23-b05b-ca86b34a158f" || modules[0]["uuid"] != "28778864-3ebe-413f-9c50-f29630e8be3e" {
		t.Errorf("pack ids changed: header %s, module %v", meta.Header.UUID, modules[0]["uuid"])
	}
}

// No test here runs the script, so this cannot prove what it does. It
// proves what it cannot do: none of the ways a script changes a world, or
// stops something happening in one, appear in it.
func TestScriptIsReadOnly(t *testing.T) {
	raw, err := files.FS.ReadFile("scripts/main.js")
	if err != nil {
		t.Fatal(err)
	}
	code := regexp.MustCompile(`(?m)^\s*//.*$`).ReplaceAllString(string(raw), "")

	for _, banned := range []string{
		"runCommand", "Events", "subscribe", "cancel",
		"DynamicProperty", "setProperty", "setRotation", "teleport", "tryTeleport",
		"spawnEntity", "spawnItem", "spawnParticle", "createExplosion",
		".remove(", ".kill(", "applyDamage", "applyImpulse", "applyKnockback", "addEffect", "addTag", "removeTag",
		"setBlock", "fillBlocks", "setWeather", "setTime", "gameRules", "scoreboard", "structureManager",
		"sendMessage", "playSound", "getComponent", "eval(", "Function(", "import(",
	} {
		if strings.Contains(code, banned) {
			t.Errorf("main.js uses %q", banned)
		}
	}
	imports := regexp.MustCompile(`(?m)^import .*$`).FindAllString(code, -1)
	want := []string{
		`import { system, world } from "@minecraft/server";`,
		`import { MOB_CAP, VERSION } from "./config.js";`,
	}
	if strings.Join(imports, "\n") != strings.Join(want, "\n") {
		t.Errorf("imports = %q", imports)
	}
	// Assigning to a property of something the server handed over is how a
	// script writes without calling anything.
	if writes := regexp.MustCompile(`\b(player|mob|world|dimension|at)\.[A-Za-z.]+\s*=[^=]`).FindAllString(code, -1); len(writes) > 0 {
		t.Errorf("main.js assigns to server objects: %q", writes)
	}
}

func itoa(n int) string {
	raw, _ := json.Marshal(n)
	return string(raw)
}
