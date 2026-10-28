package repair

import (
	"archive/tar"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/jdwillmsen/gameops/minecraft/crew/internal/arena"
)

// Snapshotter copies the lab server's world database to a local directory
// without touching a block. It uses the server's own backup protocol: `save
// hold` flushes pending chunk writes and pauses the world's file writes, `save
// query` says when the files are stable and how many bytes of each are valid,
// the copy is a tar of the db directory streamed from the container, each file
// is cut back to its listed length, and `save resume` always follows.
type Snapshotter struct {
	Console arena.Console
	// Tar starts a process that writes a tar of the world's db directory
	// (entries named db/<file>) to stdout.
	Tar   []string
	Sleep func(time.Duration)
	// Start runs the Tar command and streams its stdout until ctx ends;
	// tests replace it.
	Start func(ctx context.Context, args []string, stdout io.Writer) error
	Now   func() time.Time
}

// Snapshot is a fetched copy of the world database.
type Snapshot struct {
	Dir string    // the db directory
	At  time.Time // when the copy finished
}

// holdTries bounds the wait for `save query` to report the files ready.
const holdTries = 30

// ErrSnapshotNotReady is returned when the server never reported its files
// stable. Nothing was copied and saving was resumed.
var ErrSnapshotNotReady = errors.New("repair: server did not report the world files ready to copy")

// Fetch writes the db directory into dest, replacing any earlier db/ there.
// Cancelling ctx stops the poll or the copy; saving is resumed either way.
func (s Snapshotter) Fetch(ctx context.Context, dest string) (snap Snapshot, err error) {
	sleep := s.Sleep
	if sleep == nil {
		sleep = time.Sleep
	}
	start := s.Start
	if start == nil {
		start = runTar
	}
	now := s.Now
	if now == nil {
		now = time.Now
	}
	_, holdErr := s.Console.Exec("save hold")
	// A failed hold may still have reached the server, so resume either way.
	defer func() {
		out, rerr := s.Console.Exec("save resume")
		if rerr == nil && strings.Contains(strings.ToLower(out), "unknown command") {
			rerr = fmt.Errorf("server said %q", strings.TrimSpace(out))
		}
		if rerr != nil {
			err = errors.Join(err, fmt.Errorf("repair: save resume failed, saving may still be held: %w", rerr))
		}
	}()
	if holdErr != nil {
		return Snapshot{}, fmt.Errorf("repair: save hold: %w", holdErr)
	}
	var lengths map[string]int64
	for i := 0; i < holdTries && lengths == nil; i++ {
		if err = ctx.Err(); err != nil {
			return Snapshot{}, fmt.Errorf("repair: snapshot cancelled: %w", err)
		}
		out, qerr := s.Console.Exec("save query")
		if qerr != nil {
			return Snapshot{}, fmt.Errorf("repair: save query: %w", qerr)
		}
		// The ready line and the file list can arrive in separate log reads;
		// asking again repeats both.
		if strings.Contains(strings.ToLower(out), "ready to be copied") {
			lengths = ParseSaveQuery(out)
		}
		if lengths == nil {
			sleep(time.Second)
		}
	}
	if lengths == nil {
		return Snapshot{}, ErrSnapshotNotReady
	}
	pr, pw := io.Pipe()
	done := make(chan error, 1)
	go func() {
		cerr := start(ctx, s.Tar, pw)
		pw.CloseWithError(cerr)
		done <- cerr
	}()
	if err = extractDB(pr, dest); err != nil {
		_, _ = io.Copy(io.Discard, pr)
		<-done
		return Snapshot{}, err
	}
	if err = <-done; err != nil {
		return Snapshot{}, fmt.Errorf("repair: copy world: %w", err)
	}
	dir := filepath.Join(dest, "db")
	if err = truncateToListed(dir, lengths); err != nil {
		return Snapshot{}, err
	}
	return Snapshot{Dir: dir, At: now()}, nil
}

// ParseSaveQuery reads the file list from a `save query` reply: entries of
// `<path>:<bytes>` separated by commas, on one line or several. It returns the
// valid length per base name, or nil when the reply lists none.
func ParseSaveQuery(reply string) map[string]int64 {
	out := map[string]int64{}
	for _, tok := range strings.FieldsFunc(reply, func(r rune) bool { return r == ',' || r == '\n' || r == '\r' }) {
		tok = strings.TrimSpace(tok)
		i := strings.LastIndex(tok, ":")
		if i < 0 {
			continue
		}
		n, err := strconv.ParseInt(strings.TrimSpace(tok[i+1:]), 10, 64)
		if err != nil || n < 0 {
			continue
		}
		name := path.Base(strings.TrimSpace(tok[:i]))
		// A log prefix such as "[INFO] " leaves a space before the first path.
		if j := strings.LastIndex(name, " "); j >= 0 {
			name = name[j+1:]
		}
		if name == "" || name == "." || name == "/" {
			continue
		}
		out[name] = n
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

func isDBFile(name string) bool {
	return strings.HasSuffix(name, ".ldb") || strings.HasSuffix(name, ".log") ||
		strings.HasPrefix(name, "MANIFEST") || name == "CURRENT"
}

// truncateToListed cuts each listed file to the length the server vouched for
// (the tail past it may be a write that began after the hold). A listed
// database file that is missing or shorter than listed means the copy is not
// the snapshot; listed names that are not database files are ignored.
func truncateToListed(dir string, lengths map[string]int64) error {
	for name, n := range lengths {
		if !isDBFile(name) {
			continue
		}
		p := filepath.Join(dir, name)
		fi, err := os.Stat(p)
		if err != nil {
			return fmt.Errorf("repair: listed file %s is missing from the copy", name)
		}
		if fi.Size() < n {
			return fmt.Errorf("repair: %s is %d bytes in the copy but the server listed %d", name, fi.Size(), n)
		}
		if fi.Size() > n {
			if err := os.Truncate(p, n); err != nil {
				return err
			}
		}
	}
	return nil
}

func runTar(ctx context.Context, args []string, stdout io.Writer) error {
	cmd := exec.CommandContext(ctx, args[0], args[1:]...)
	cmd.Stdout = stdout
	var stderr strings.Builder
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("%w: %s", err, strings.TrimSpace(stderr.String()))
	}
	return nil
}

// extractDB unpacks a tar of db/<file> entries into dest/db, first emptying
// any db/ left there by an earlier copy so files of two snapshots never mix.
// Anything else in the tar, a path that climbs out or a nested name, is
// refused: the stream comes from a container.
func extractDB(r io.Reader, dest string) error {
	db := filepath.Join(dest, "db")
	if err := os.RemoveAll(db); err != nil {
		return err
	}
	if err := os.MkdirAll(db, 0o755); err != nil {
		return err
	}
	tr := tar.NewReader(r)
	n := 0
	for {
		h, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return fmt.Errorf("repair: read world tar: %w", err)
		}
		if h.Typeflag == tar.TypeDir {
			continue
		}
		dir, name := path.Split(path.Clean(h.Name))
		if h.Typeflag != tar.TypeReg || dir != "db/" || name == "" || strings.Contains(name, "..") || strings.ContainsAny(name, `/\`) {
			return fmt.Errorf("repair: unexpected entry %q in world tar", h.Name)
		}
		f, err := os.OpenFile(filepath.Join(db, filepath.Base(name)), os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o644)
		if err != nil {
			return err
		}
		_, cerr := io.Copy(f, tr)
		if err := errors.Join(cerr, f.Close()); err != nil {
			return err
		}
		n++
	}
	if n == 0 {
		return fmt.Errorf("repair: world tar held no files")
	}
	return nil
}
