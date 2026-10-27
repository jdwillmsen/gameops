package trainer

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/jdwillmsen/gameops/minecraft/crew/internal/tactics"
)

// ErrLocked means another trainer already holds the family in this store.
var ErrLocked = errors.New("trainer: family is already being trained")

// Lock takes the exclusive training lock for one family of the store and
// returns its release. Two trainers on the same champion would each promote
// from the same parent and silently overwrite the other's history entry, so a
// second one fails at once instead of waiting. The lock is an advisory flock
// on a file, which the kernel drops when its holder dies: a crashed trainer
// leaves nothing stale to clean up. The holder's PID is written into the file
// only to make the refusal readable.
func (s *Store) Lock(f tactics.Family) (release func(), err error) {
	fd, err := s.familyDir(f)
	if err != nil {
		return nil, err
	}
	if err := os.MkdirAll(fd, 0o755); err != nil {
		return nil, err
	}
	path := filepath.Join(fd, "lock")
	file, err := os.OpenFile(path, os.O_RDWR|os.O_CREATE, 0o644)
	if err != nil {
		return nil, err
	}
	if err := flockExclusive(file); err != nil {
		holder := holderPID(file)
		file.Close()
		if errors.Is(err, errWouldBlock) {
			if holder != "" {
				return nil, fmt.Errorf("%w: %s in %s is held by pid %s", ErrLocked, f, s.dir, holder)
			}
			return nil, fmt.Errorf("%w: %s in %s", ErrLocked, f, s.dir)
		}
		return nil, err
	}
	// Best effort: a failed PID write only costs a less helpful message.
	if err := file.Truncate(0); err == nil {
		file.WriteAt([]byte(strconv.Itoa(os.Getpid())+"\n"), 0)
	}
	return func() {
		// Closing drops the flock; the file stays so the next holder reuses it.
		file.Close()
	}, nil
}

func holderPID(file *os.File) string {
	b := make([]byte, 32)
	n, _ := file.ReadAt(b, 0)
	return strings.TrimSpace(string(b[:n]))
}
