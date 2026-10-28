package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"

	"github.com/jdwillmsen/gameops/minecraft/crew/internal/tactics"
)

// ProfileSink hands a candidate Profile to lab units. Clear must put the
// units back on the shipped behaviour and be safe to call twice, because it
// runs on every exit path.
type ProfileSink interface {
	Push(ctx context.Context, units []string, p tactics.Profile) error
	Clear(ctx context.Context, units []string) error
	// LabSquads names the squads the units fight in; the bridge holds each
	// one's lock for the whole fight. A sink with no shared state names none.
	LabSquads(units []string) ([]string, error)
	// Confirm reads back what the units are served and fails unless it is
	// exactly p, so a lost or reordered PUT is never scored as p.
	Confirm(ctx context.Context, units []string, p tactics.Profile) error
}

// unitFile keeps a unit name from becoming a path.
var unitFile = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_.-]{0,63}$`)

// FileSink drops <dir>/<unit>.json for each unit so a dry run can show what
// would be pushed. Nothing reads these files: the lab units take their
// profile from the squad route, so a fight scored against this sink would
// measure the shipped behaviour under the candidate's name. It is therefore
// reachable only behind --dry-run, which never fights.
type FileSink struct{ Dir string }

func (FileSink) LabSquads([]string) ([]string, error) { return nil, nil }

func (FileSink) Confirm(context.Context, []string, tactics.Profile) error { return nil }

func (s FileSink) path(unit string) (string, error) {
	if !unitFile.MatchString(unit) {
		return "", fmt.Errorf("trainbridge: %q is not a valid unit name", unit)
	}
	return filepath.Join(s.Dir, unit+".json"), nil
}

func (s FileSink) Push(ctx context.Context, units []string, p tactics.Profile) error {
	body, err := json.MarshalIndent(p, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(s.Dir, 0o755); err != nil {
		return err
	}
	for _, u := range units {
		if err := ctx.Err(); err != nil {
			return err
		}
		path, err := s.path(u)
		if err != nil {
			return err
		}
		if err := writeAtomic(path, body); err != nil {
			return err
		}
	}
	return nil
}

func (s FileSink) Clear(_ context.Context, units []string) error {
	var first error
	for _, u := range units {
		path, err := s.path(u)
		if err == nil {
			err = os.Remove(path)
			if os.IsNotExist(err) {
				err = nil
			}
		}
		if err != nil && first == nil {
			first = err
		}
	}
	return first
}

// writeAtomic renames a finished file into place so a unit polling the
// directory never reads half a profile.
func writeAtomic(path string, body []byte) error {
	tmp, err := os.CreateTemp(filepath.Dir(path), ".profile-*")
	if err != nil {
		return err
	}
	_, werr := tmp.Write(body)
	cerr := tmp.Close()
	if err := errors.Join(werr, cerr); err != nil {
		os.Remove(tmp.Name())
		return err
	}
	if err := os.Chmod(tmp.Name(), 0o644); err != nil {
		os.Remove(tmp.Name())
		return err
	}
	if err := os.Rename(tmp.Name(), path); err != nil {
		os.Remove(tmp.Name())
		return err
	}
	return nil
}
