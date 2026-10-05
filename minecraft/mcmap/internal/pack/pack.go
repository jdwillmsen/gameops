// Package pack installs the live-position behaviour pack into a Bedrock
// server's data directory, as an init step that runs before the server.
//
// It fails open. An init step that exits non-zero keeps the game server from
// starting at all, and a map layer is never worth that: on 2026-09-06 a
// sidecar's dependency became the server's and the world was offline for 40
// hours. So everything this cannot do is logged and skipped, the server
// starts without the pack, and the missing live data is what raises the
// alarm. The single exception is ErrHalfWritten, which can happen once.
package pack

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	files "github.com/jdwillmsen/gameops/minecraft/mcmap/pack"
)

const (
	// DirName must not start with vanilla, chemistry or experimental: the
	// server image deletes those from behavior_packs on every upgrade.
	DirName = "mcmap-live"

	DefaultMobCap = 1000
	// MaxMobCap is the largest load the pack's self-throttle was measured
	// against. A typo with three more zeros should not be the first test of
	// anything larger.
	MaxMobCap = 5000

	registryName = "world_behavior_packs.json"
	configName   = "scripts/config.js"
	// stagingName sits beside behavior_packs rather than inside it, on the
	// same volume so that moving a pack into place is a rename, and out of
	// the directory the server scans so that it never sees two packs with
	// one id.
	stagingName = ".mcmap-live-staging"
)

// ErrHalfWritten means the installed pack was moved aside for its
// replacement and could be neither replaced nor put back, so the world now
// registers a pack that is not there. It is the one failure reported as a
// failure, because the retry that buys is what repairs it: the next attempt
// finds nothing to move aside, so it either installs or skips like any
// other. The server would have started regardless; it ignores a registered
// pack it cannot find.
var ErrHalfWritten = errors.New("the pack is half-installed and could not be rolled back")

type manifest struct {
	Header struct {
		UUID    string `json:"uuid"`
		Version [3]int `json:"version"`
	} `json:"header"`
}

// entry is one line of a world's pack list. Everything but the id is kept
// as it was found, so another pack's entry survives byte for byte.
type entry struct {
	PackID  string          `json:"pack_id"`
	Version json.RawMessage `json:"version"`
}

type installer struct {
	dataDir string
	level   string
	mobCap  int
	logger  *slog.Logger
	// rename and write are os.Rename and writeSynced, replaceable so that a
	// test can fail one part of the way through.
	rename func(oldpath, newpath string) error
	write  func(path string, content []byte) error
}

// Version identifies the pack as installed with this mob cap. It changes
// when any file of the pack does, the generated one included.
func Version(mobCap int) string {
	sum := sha256.New()
	for _, name := range []string{"manifest.json", "scripts/main.js"} {
		fmt.Fprintf(sum, "%s\x00%s\x00", name, embedded(name))
	}
	fmt.Fprintf(sum, "cap\x00%d\x00", mobCap)
	return hex.EncodeToString(sum.Sum(nil))[:12]
}

// Install puts the pack into the server under dataDir and registers it with
// the world named level. A pack already installed with this content is left
// untouched. Every error but ErrHalfWritten means nothing the server reads
// was changed.
func Install(dataDir, level string, mobCap int, logger *slog.Logger) error {
	in := &installer{dataDir: dataDir, level: level, mobCap: mobCap, logger: logger, rename: os.Rename, write: writeSynced}
	return in.install()
}

// Uninstall takes the pack out of the world's list and then off the disk.
// It exists because removing the init step does not remove the pack: both
// the files and the registration live on the server's volume.
func Uninstall(dataDir, level string, logger *slog.Logger) error {
	in := &installer{dataDir: dataDir, level: level, logger: logger, rename: os.Rename, write: writeSynced}
	return in.uninstall()
}

// Run is the install-pack and uninstall-pack subcommands. It returns the
// process exit status, which is zero for everything except ErrHalfWritten.
func Run(command string, getenv func(string) string, logger *slog.Logger) (status int) {
	// A panic would exit 2 and hold the server exactly as an error would.
	defer func() {
		if r := recover(); r != nil {
			logger.Error("pack step panicked; the server starts without the change", "command", command, "panic", fmt.Sprint(r))
			status = 0
		}
	}()

	dataDir, level := getenv("DATA_DIR"), getenv("LEVEL_NAME")
	if dataDir == "" || level == "" {
		logger.Error("pack step skipped: DATA_DIR and LEVEL_NAME are both required", "command", command)
		return 0
	}

	var err error
	switch command {
	case "uninstall-pack":
		err = Uninstall(dataDir, level, logger)
	default:
		err = Install(dataDir, level, mobCap(getenv("PACK_MOB_CAP"), logger), logger)
	}
	return exitStatus(command, err, logger)
}

// exitStatus is where failing open is decided: one error holds the server,
// and every other is logged and let through.
func exitStatus(command string, err error, logger *slog.Logger) int {
	switch {
	case err == nil:
		return 0
	case errors.Is(err, ErrHalfWritten):
		logger.Error("pack step failed and could not roll back; holding the server", "command", command, "error", err.Error())
		return 1
	default:
		logger.Error("pack step skipped; the server starts without the change", "command", command, "error", err.Error())
		return 0
	}
}

func mobCap(raw string, logger *slog.Logger) int {
	if strings.TrimSpace(raw) == "" {
		return DefaultMobCap
	}
	n, err := strconv.Atoi(strings.TrimSpace(raw))
	switch {
	case err != nil || n <= 0:
		logger.Warn("PACK_MOB_CAP is not a positive number; using the default", "value", raw, "default", DefaultMobCap)
		return DefaultMobCap
	case n > MaxMobCap:
		logger.Warn("PACK_MOB_CAP is above the largest supported cap; using that", "value", raw, "max", MaxMobCap)
		return MaxMobCap
	}
	return n
}

func (in *installer) install() error {
	worldDir, err := in.worldDir()
	if err != nil {
		return err
	}
	meta, err := readManifest()
	if err != nil {
		return err
	}
	// Read before anything is written: a list that cannot be understood
	// cannot be merged into, and the pack is no use unregistered.
	entries, err := readRegistry(worldDir)
	if err != nil {
		return err
	}

	version := Version(in.mobCap)
	want := map[string][]byte{
		"manifest.json":   embedded("manifest.json"),
		"scripts/main.js": embedded("scripts/main.js"),
		configName:        fmt.Appendf(nil, "export const MOB_CAP = %d;\nexport const VERSION = %q;\n", in.mobCap, version),
	}
	packDir := filepath.Join(in.dataDir, "behavior_packs", DirName)
	merged, registered := register(entries, meta)
	current := installed(packDir, want)
	if current && registered {
		in.logger.Info("pack already installed", "version", version, "mob_cap", in.mobCap)
		return nil
	}

	if !current {
		if err := in.replacePack(packDir, want); err != nil {
			return err
		}
	}
	// The pack goes in before its registration, never after: an
	// unregistered pack on disk is ignored by the server, a registered one
	// that is missing is an error in its log on every start.
	if !registered {
		if err := in.writeRegistry(worldDir, merged); err != nil {
			return fmt.Errorf("pack written but not registered, so the server will not load it: %w", err)
		}
	}
	in.logger.Info("pack installed", "version", version, "mob_cap", in.mobCap, "pack_dir", packDir, "pack_id", meta.Header.UUID)
	return nil
}

func (in *installer) uninstall() error {
	worldDir, err := in.worldDir()
	if err != nil {
		return err
	}
	meta, err := readManifest()
	if err != nil {
		return err
	}
	entries, err := readRegistry(worldDir)
	if err != nil {
		return err
	}
	kept := make([]json.RawMessage, 0, len(entries))
	for _, raw := range entries {
		if packID(raw) != meta.Header.UUID {
			kept = append(kept, raw)
		}
	}
	// Unregistered first, for the same reason install registers last.
	if len(kept) != len(entries) {
		if err := in.writeRegistry(worldDir, kept); err != nil {
			return fmt.Errorf("pack left installed: %w", err)
		}
	}
	if err := os.RemoveAll(filepath.Join(in.dataDir, "behavior_packs", DirName)); err != nil {
		return fmt.Errorf("pack unregistered but its files remain: %w", err)
	}
	_ = os.RemoveAll(filepath.Join(in.dataDir, stagingName))
	in.logger.Info("pack uninstalled", "pack_id", meta.Header.UUID)
	return nil
}

// worldDir refuses a world that is not there yet. A server that has never
// run creates it on first start, and a registration written ahead of that
// would be a world directory the server did not make.
func (in *installer) worldDir() (string, error) {
	if in.level != filepath.Base(in.level) || in.level == "." || in.level == ".." {
		return "", fmt.Errorf("level name %q is not a directory name", in.level)
	}
	dir := filepath.Join(in.dataDir, "worlds", in.level)
	info, err := os.Stat(dir)
	if err != nil {
		return "", fmt.Errorf("world directory: %w", err)
	}
	if !info.IsDir() {
		return "", fmt.Errorf("world directory %s is not a directory", dir)
	}
	return dir, nil
}

// replacePack swaps a complete new pack directory in for the old one, so
// the server never finds a pack with some files from each.
func (in *installer) replacePack(packDir string, want map[string][]byte) error {
	staging := filepath.Join(in.dataDir, stagingName)
	if err := os.RemoveAll(staging); err != nil {
		return fmt.Errorf("clear staging: %w", err)
	}
	fresh, old := filepath.Join(staging, "new"), filepath.Join(staging, "old")
	for name, content := range want {
		path := filepath.Join(fresh, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			_ = os.RemoveAll(staging)
			return fmt.Errorf("stage pack: %w", err)
		}
		if err := in.write(path, content); err != nil {
			_ = os.RemoveAll(staging)
			return fmt.Errorf("stage pack: %w", err)
		}
	}
	if err := os.MkdirAll(filepath.Dir(packDir), 0o755); err != nil {
		_ = os.RemoveAll(staging)
		return fmt.Errorf("pack directory: %w", err)
	}

	replaced := false
	if _, err := os.Lstat(packDir); err == nil {
		if err := in.rename(packDir, old); err != nil {
			_ = os.RemoveAll(staging)
			return fmt.Errorf("move the installed pack aside: %w", err)
		}
		replaced = true
	}
	if err := in.rename(fresh, packDir); err != nil {
		if replaced {
			if back := in.rename(old, packDir); back != nil {
				// Staging is left alone: it holds the only copy of both.
				return fmt.Errorf("%w: install: %v; restore: %v", ErrHalfWritten, err, back)
			}
		}
		_ = os.RemoveAll(staging)
		return fmt.Errorf("move the pack into place: %w", err)
	}
	// Past this point the pack is installed; what is left is tidying, and a
	// failure to tidy is not a failure to install.
	syncDir(filepath.Dir(packDir))
	if err := os.RemoveAll(staging); err != nil {
		in.logger.Warn("pack installed but staging could not be removed", "error", err.Error())
	}
	return nil
}

// writeRegistry replaces the world's pack list in one rename, so the server
// reads either the old list or the new one and never part of either.
func (in *installer) writeRegistry(worldDir string, entries []json.RawMessage) error {
	body, err := json.MarshalIndent(entries, "", "\t")
	if err != nil {
		return fmt.Errorf("encode %s: %w", registryName, err)
	}
	path := filepath.Join(worldDir, registryName)
	tmp := path + ".mcmap-tmp"
	if err := in.write(tmp, append(body, '\n')); err != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("write %s: %w", registryName, err)
	}
	if err := in.rename(tmp, path); err != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("replace %s: %w", registryName, err)
	}
	syncDir(worldDir)
	return nil
}

func readManifest() (manifest, error) {
	var meta manifest
	if err := json.Unmarshal(embedded("manifest.json"), &meta); err != nil || meta.Header.UUID == "" {
		return meta, fmt.Errorf("the embedded manifest has no pack id: %v", err)
	}
	return meta, nil
}

// readRegistry returns the world's pack list, which is empty for a world
// that has never had a pack: the server does not write the file itself.
func readRegistry(worldDir string) ([]json.RawMessage, error) {
	raw, err := os.ReadFile(filepath.Join(worldDir, registryName))
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", registryName, err)
	}
	var entries []json.RawMessage
	if err := json.Unmarshal(raw, &entries); err != nil {
		return nil, fmt.Errorf("%s will not parse and was left as it is: %w", registryName, err)
	}
	for _, e := range entries {
		var probe entry
		if err := json.Unmarshal(e, &probe); err != nil {
			return nil, fmt.Errorf("%s holds something that is not a pack entry and was left as it is: %w", registryName, err)
		}
	}
	return entries, nil
}

// register returns the list with this pack in it, and whether it was there
// already at this version.
func register(entries []json.RawMessage, meta manifest) (merged []json.RawMessage, registered bool) {
	version, _ := json.Marshal(meta.Header.Version)
	ours, _ := json.Marshal(entry{PackID: meta.Header.UUID, Version: version})

	merged = make([]json.RawMessage, 0, len(entries)+1)
	found := false
	for _, raw := range entries {
		if packID(raw) != meta.Header.UUID {
			merged = append(merged, raw)
			continue
		}
		if found {
			continue
		}
		found = true
		var have entry
		_ = json.Unmarshal(raw, &have)
		var compact bytes.Buffer
		if json.Compact(&compact, have.Version) == nil && bytes.Equal(compact.Bytes(), version) {
			registered = true
			merged = append(merged, raw)
			continue
		}
		merged = append(merged, ours)
	}
	if !found {
		merged = append(merged, ours)
	}
	return merged, registered
}

func packID(raw json.RawMessage) string {
	var e entry
	_ = json.Unmarshal(raw, &e)
	return e.PackID
}

// installed reports whether packDir holds exactly these files with exactly
// this content.
func installed(packDir string, want map[string][]byte) bool {
	found := 0
	err := filepath.WalkDir(packDir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		rel, err := filepath.Rel(packDir, path)
		if err != nil {
			return err
		}
		content, ok := want[filepath.ToSlash(rel)]
		if !ok || !d.Type().IsRegular() {
			return fs.ErrInvalid
		}
		have, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		if !bytes.Equal(have, content) {
			return fs.ErrInvalid
		}
		found++
		return nil
	})
	return err == nil && found == len(want)
}

func embedded(name string) []byte {
	content, err := files.FS.ReadFile(name)
	if err != nil {
		// The names are compiled in beside the files, so this is a build
		// that could not have passed its tests.
		panic(err)
	}
	return content
}

// writeSynced writes a file the server can read whatever umask the init
// step runs under, and flushes it so that a rename that survives a crash
// never names an empty file.
func writeSynced(path string, content []byte) error {
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o644)
	if err != nil {
		return err
	}
	if _, err := f.Write(content); err != nil {
		_ = f.Close()
		return err
	}
	if err := f.Chmod(0o644); err != nil {
		_ = f.Close()
		return err
	}
	if err := f.Sync(); err != nil {
		_ = f.Close()
		return err
	}
	return f.Close()
}

// syncDir makes a rename durable. It is best effort: the rename has already
// happened, and failing here would report an install that worked as one
// that did not.
func syncDir(dir string) {
	if d, err := os.Open(dir); err == nil {
		_ = d.Sync()
		_ = d.Close()
	}
}
