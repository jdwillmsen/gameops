//go:build unix

package generations

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"syscall"
	"testing"
	"time"
)

const (
	crashStepEnv = "MCMAP_GENERATIONS_CRASH_STEP"
	crashRootEnv = "MCMAP_GENERATIONS_CRASH_ROOT"
	crashSrcEnv  = "MCMAP_GENERATIONS_CRASH_SRC"
)

// steps are every point at which a capture has changed the volume, plus
// the long one in the middle of it. The invariant below is checked after
// the process is killed at each.
var steps = []string{"linking", "built", "retired", "installed", "switched"}

// A node that dies mid-capture is the event this whole thing exists for, so
// it is tested the way it happens: a real process is SIGKILLed partway
// through a real capture, with no chance to unwind, and the volume it
// leaves behind is then opened as a restart would open it. There must
// always be a whole world to restore from, and it must be whole — a world
// missing the files the kill interrupted would be worse than none, because
// nothing would say so.
//
// What this does not show is durability: SIGKILL loses no page cache, so
// surviving the machine itself stopping rests on the ordering above and on
// the flushes in settle, not on this test.
func TestCrash_AKilledCaptureAlwaysLeavesAWholeGenerationToRestoreFrom(t *testing.T) {
	if step := os.Getenv(crashStepEnv); step != "" {
		crashAt(step, os.Getenv(crashRootEnv), os.Getenv(crashSrcEnv))
		return
	}
	parent := t.Name()
	for _, step := range steps {
		t.Run(step, func(t *testing.T) {
			src, root := t.TempDir(), filepath.Join(t.TempDir(), "generations")
			s := open(t, root)
			// Two whole snapshots, so both slots are in use and the capture
			// the child is killed in has one to overwrite.
			mirror(t, src, "one", "000005.ldb")
			capture(t, s, src, noon, Whole)
			mirror(t, src, "two", "000005.ldb", "000006.ldb")
			capture(t, s, src, noon.Add(15*time.Minute), Whole)
			held := contents(t, s.Look().Current.Path)
			delete(held, markerName)
			mirror(t, src, "three", "000005.ldb", "000006.ldb", "000007.ldb")
			arriving := contents(t, src)

			child := exec.Command(os.Args[0], "-test.run=^"+parent+"$")
			child.Env = append(os.Environ(),
				crashStepEnv+"="+step, crashRootEnv+"="+root, crashSrcEnv+"="+src)
			out, err := child.CombinedOutput()
			kill(t, step, out, err)

			// Exactly what the map does on its next start.
			v := open(t, root).Look()
			if v.Current == nil {
				t.Fatalf("killed at %q: nothing left to restore from\n%s", step, out)
			}
			got := contents(t, v.Current.Path)
			delete(got, markerName)
			switch {
			case equal(got, held):
				// The switch had not happened: the world is one snapshot older.
			case equal(got, arriving):
				// The switch had happened: the world is the newest snapshot.
			default:
				t.Fatalf("killed at %q: the current generation is neither snapshot whole: %v", step, keys(got))
			}
			if v.Current.Files != len(got) {
				t.Errorf("killed at %q: marker claims %d files, directory holds %d", step, v.Current.Files, len(got))
			}
			// A restart gives back the room an interrupted capture took.
			entries, _ := os.ReadDir(root)
			for _, name := range names(entries) {
				if !slices.Contains([]string{"a", "b", currentLink, damagedDir}, name) {
					t.Errorf("killed at %q: %q survived the restart", step, name)
				}
			}
		})
	}
}

// crashAt runs one capture in a child process and kills that process dead
// at the named step.
func crashAt(step, root, src string) {
	s, err := Open(root, quiet())
	if err != nil {
		panic(err)
	}
	die := func(reached string) {
		fmt.Println("reached", reached)
		os.Stdout.Sync()
		_ = syscall.Kill(os.Getpid(), syscall.SIGKILL)
	}
	// Linking the world is where nearly all of a capture's time goes, so
	// it is where a kill is most likely to land.
	if step == "linking" {
		linked := 0
		s.link = func(from, to string) error {
			if linked++; linked > 2 {
				die(step)
			}
			return os.Link(from, to)
		}
	}
	s.pause = func(reached string) {
		if reached == step {
			die(reached)
		}
	}
	if _, err := s.Capture(context.Background(), src, noon.Add(30*time.Minute), Whole); err != nil {
		panic(err)
	}
	panic("the capture finished without reaching " + step)
}

func kill(t *testing.T, step string, out []byte, err error) {
	t.Helper()
	var exit *exec.ExitError
	if !errors.As(err, &exit) {
		t.Fatalf("step %q: child exited %v\n%s", step, err, out)
	}
	if status, ok := exit.Sys().(syscall.WaitStatus); !ok || status.Signal() != syscall.SIGKILL {
		t.Fatalf("step %q: child did not die of SIGKILL: %v\n%s", step, exit, out)
	}
}
