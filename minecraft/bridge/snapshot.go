package main

import (
	"archive/tar"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"path"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
)

// What the server answers the save commands with, as Bedrock Dedicated
// Server 1.26.52 prints them. Checked against a live server: a resume takes
// effect even milliseconds into a hold, while the save is still running, and
// saveNothingHeld is what a resume (or a query) gets when nothing is paused.
// So either saveResumed or saveNothingHeld after a resume means saving is on.
const (
	saveReady       = "Data saved. Files are now ready to be copied."
	saveHeld        = "The command is already running"
	saveResumed     = "Changes to the world are resumed."
	saveNothingHeld = "A previous save has not been completed."
)

// Entry names the stream adds around the world files. A world file can never
// collide with them: parseSaveQuery rejects any name that is not inside a
// world directory.
const (
	snapshotManifestName = "snapshot.json"
	snapshotCompleteName = "snapshot.ok"
)

var (
	metricSnapshots = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "mc_console_bridge_snapshots_total",
		Help: "World snapshots requested, by how they ended.",
	}, []string{"result"})
	metricSnapshotHoldSeconds = promauto.NewHistogram(prometheus.HistogramOpts{
		Name:    "mc_console_bridge_snapshot_hold_seconds",
		Help:    "How long world saving stayed paused for a snapshot.",
		Buckets: []float64{1, 2, 5, 10, 20, 30, 60, 120, 300},
	})
	metricSnapshotBytes = promauto.NewCounter(prometheus.CounterOpts{
		Name: "mc_console_bridge_snapshot_bytes_total",
		Help: "World file bytes streamed to snapshot callers.",
	})
	metricSnapshotResumeOwed = promauto.NewGauge(prometheus.GaugeOpts{
		Name: "mc_console_bridge_snapshot_resume_owed",
		Help: "1 while a snapshot's pause may still be in place because its resume has not been confirmed.",
	})
)

var (
	errSnapshotBusy   = errors.New("a snapshot is already in progress")
	errHoldActive     = errors.New("world saving is paused by something else")
	errNotReady       = errors.New("the server did not finish saving in time")
	errResumeOwed     = errors.New("an earlier snapshot's resume is still unconfirmed")
	errSnapshotClosed = errors.New("the bridge is shutting down")
)

type snapshotFile struct {
	Name string `json:"name"`
	Size int64  `json:"size"`
}

type snapshotManifest struct {
	Files []snapshotFile `json:"files"`
}

type snapshotRequest struct {
	// Have maps a world file to the size the caller already holds.
	Have map[string]int64 `json:"have"`
}

type commandSender interface {
	SendCommand(ctx context.Context, cmd string) (string, error)
}

// snapshotter hands out a point-in-time copy of the world while the server
// keeps running: it pauses saving, streams the files at the lengths the
// server reports, and resumes saving.
//
// The pause is the dangerous part. A world left paused keeps every change in
// memory, so from the moment a hold may have reached the server a resume is
// owed, and stays owed -- across console reconnects, across callers -- until
// the server confirms saving is on. The one exception is a pause that is not
// ours: other jobs pause saving through a channel this process cannot see,
// and resuming under them would unfreeze the world beneath their copy.
type snapshotter struct {
	console   commandSender
	worldsDir string
	logger    *slog.Logger

	// maxHold bounds the pause; a stalled caller must not leave the world
	// accumulating unsaved changes.
	maxHold      time.Duration
	readyTimeout time.Duration
	queryEvery   time.Duration
	resumeEvery  time.Duration
	// commandBudget bounds one console command, on a clock of its own.
	commandBudget time.Duration

	busy   sync.Mutex
	owed   atomic.Bool
	closed atomic.Bool
	// stop ends when the bridge is shutting down; it cuts short any snapshot
	// in flight so its resume is sent while the console is still connected.
	stop       context.Context
	cancelStop context.CancelFunc
}

func newSnapshotter(console commandSender, worldsDir string, maxHold time.Duration, logger *slog.Logger) *snapshotter {
	stop, cancel := context.WithCancel(context.Background())
	return &snapshotter{
		console:       console,
		worldsDir:     worldsDir,
		logger:        logger,
		maxHold:       maxHold,
		readyTimeout:  30 * time.Second,
		queryEvery:    500 * time.Millisecond,
		resumeEvery:   2 * time.Second,
		commandBudget: 10 * time.Second,
		stop:          stop,
		cancelStop:    cancel,
	}
}

// serverLine is the shape of a line the server itself logs:
// "[2026-10-01 05:48:31:684 ERROR] The command is already running".
var serverLine = regexp.MustCompile(`^\[\d{4}-\d{2}-\d{2} [\d:]+ [A-Z]+\] (.*)$`)

// said reports whether the server printed message as a line of its own. The
// console carries everything the server logs, including script output and
// player names, so a line that merely contains the words does not count.
func said(out, message string) bool {
	for _, line := range strings.Split(strings.ReplaceAll(out, "\r", ""), "\n") {
		if m := serverLine.FindStringSubmatch(strings.TrimSpace(line)); m != nil && m[1] == message {
			return true
		}
	}
	return false
}

// parseSaveQuery reads a `save query` answer. ready is false until the
// server reports the save complete and the whole file list has arrived.
func parseSaveQuery(out string) (files []snapshotFile, ready bool, err error) {
	lines := strings.Split(strings.ReplaceAll(out, "\r", ""), "\n")
	for i, line := range lines {
		if !said(line, saveReady) {
			continue
		}
		// The list is the next line, and it is complete only once its own
		// newline has arrived: the last element of the split is whatever
		// followed the final newline, so it is never a finished line.
		if i+1 >= len(lines)-1 {
			return nil, false, nil
		}
		for _, entry := range strings.Split(lines[i+1], ", ") {
			colon := strings.LastIndexByte(entry, ':')
			if colon < 0 {
				return nil, false, fmt.Errorf("save query entry %q has no size", entry)
			}
			name := entry[:colon]
			size, err := strconv.ParseInt(entry[colon+1:], 10, 64)
			if err != nil || size < 0 {
				return nil, false, fmt.Errorf("save query entry %q has a bad size", entry)
			}
			if err := checkWorldPath(name); err != nil {
				return nil, false, err
			}
			files = append(files, snapshotFile{Name: name, Size: size})
		}
		return files, true, nil
	}
	return nil, false, nil
}

// checkWorldPath accepts only a clean relative path inside a world
// directory. The names come from the server's own console, but they become
// file opens and tar entry names, so they are not taken on trust.
func checkWorldPath(name string) error {
	if name == "" || strings.ContainsAny(name, "\\\x00") || path.IsAbs(name) || path.Clean(name) != name ||
		name == ".." || strings.HasPrefix(name, "../") || !strings.Contains(name, "/") {
		return fmt.Errorf("save query names a path outside a world directory: %q", name)
	}
	return nil
}

// send runs one console command on a context of its own. The caller's
// context must not reach the console write: a write on a cancelled context
// closes the whole websocket, and a caller going away is exactly when the
// resume has to get through.
func (s *snapshotter) send(cmd string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), s.commandBudget)
	defer cancel()
	return s.console.SendCommand(ctx, cmd)
}

func (s *snapshotter) setOwed(owed bool) {
	s.owed.Store(owed)
	if owed {
		metricSnapshotResumeOwed.Set(1)
	} else {
		metricSnapshotResumeOwed.Set(0)
	}
}

func (s *snapshotter) resumeOwed() bool { return s.owed.Load() }

// hold pauses saving and returns the consistent file set. The caller must
// call the returned release exactly once; it resumes saving.
func (s *snapshotter) hold(ctx context.Context) (files []snapshotFile, release func(), err error) {
	if !s.busy.TryLock() {
		return nil, nil, errSnapshotBusy
	}
	handedOver := false
	defer func() {
		if !handedOver {
			s.busy.Unlock()
		}
	}()

	if s.closed.Load() {
		return nil, nil, errSnapshotClosed
	}
	// Never pause on top of a pause that may still be in place.
	if s.owed.Load() && !s.resume() {
		return nil, nil, errResumeOwed
	}

	started := time.Now()
	s.setOwed(true)
	out, err := s.send("save hold")
	switch {
	case errors.Is(err, ErrNotConnected):
		// Nothing was written to the console.
		s.setOwed(false)
		return nil, nil, err
	case said(out, saveHeld):
		s.setOwed(false)
		return nil, nil, errHoldActive
	case err != nil:
		// The command may have reached the server before the failure.
		s.resume()
		return nil, nil, fmt.Errorf("save hold: %w", err)
	}

	files, err = s.waitReady(ctx)
	if errors.Is(err, errHoldActive) {
		s.setOwed(false)
		return nil, nil, err
	}
	if err != nil {
		s.resume()
		return nil, nil, err
	}

	handedOver = true
	return files, func() {
		s.resume()
		metricSnapshotHoldSeconds.Observe(time.Since(started).Seconds())
		s.busy.Unlock()
	}, nil
}

// waitReady polls until the save is complete. It also keeps watching for the
// server refusing a hold, for two cases a single reply cannot settle: the
// answer to our own hold arriving late, and another job trying to pause
// during this snapshot. In both, something else believes the pause is its
// own and will resume it when its copy is done; resuming here would cut that
// copy's ground from under it.
func (s *snapshotter) waitReady(ctx context.Context) ([]snapshotFile, error) {
	deadline := time.Now().Add(s.readyTimeout)
	for {
		out, err := s.send("save query")
		if err != nil {
			return nil, fmt.Errorf("save query: %w", err)
		}
		if said(out, saveHeld) {
			return nil, errHoldActive
		}
		files, ready, err := parseSaveQuery(out)
		if err != nil {
			return nil, err
		}
		if ready {
			return files, nil
		}
		if time.Now().After(deadline) {
			return nil, errNotReady
		}
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(s.queryEvery):
		}
	}
}

// resume turns saving back on and reports whether the server confirmed it.
// An unconfirmed resume stays owed; settle keeps trying.
func (s *snapshotter) resume() bool {
	const attempts = 5
	for i := range attempts {
		out, err := s.send("save resume")
		if err == nil && (said(out, saveResumed) || said(out, saveNothingHeld)) {
			s.setOwed(false)
			return true
		}
		if errors.Is(err, ErrNotConnected) {
			// The websocket drops for a second or more on any read error
			// while the server, and its pause, carry on.
			s.logger.Warn("save resume could not be sent: console not connected; it stays owed")
			return false
		}
		s.logger.Warn("save resume not confirmed, retrying", "attempt", i+1, "error", err)
		time.Sleep(s.resumeEvery)
	}
	s.logger.Error("save resume was never confirmed; world saving may still be paused")
	return false
}

// settle pays an owed resume if no snapshot is running.
func (s *snapshotter) settle() {
	if !s.owed.Load() || !s.busy.TryLock() {
		return
	}
	defer s.busy.Unlock()
	if s.owed.Load() && s.resume() {
		s.logger.Info("owed save resume confirmed")
	}
}

// Run keeps trying an owed resume until ctx ends.
func (s *snapshotter) Run(ctx context.Context) {
	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			s.settle()
		}
	}
}

// Close cuts short any snapshot in flight, waits for it to resume saving,
// and refuses new ones. It must be called while the console is still
// connected.
func (s *snapshotter) Close() {
	s.closed.Store(true)
	s.cancelStop()
	s.busy.Lock()
	defer s.busy.Unlock()
	if s.owed.Load() {
		s.resume()
	}
}

// needs reports whether the caller lacks a file. LevelDB table files never
// change once written, so one the caller holds at the same size is the same
// file; everything else is rewritten in place and is always sent.
func needs(f snapshotFile, have map[string]int64) bool {
	if !strings.HasSuffix(f.Name, ".ldb") {
		return true
	}
	size, ok := have[f.Name]
	return !ok || size != f.Size
}

// stream writes the manifest, the files the caller lacks, and then the
// completion marker. A stream without the marker is not a snapshot.
func (s *snapshotter) stream(ctx context.Context, w io.Writer, files []snapshotFile, have map[string]int64) (int64, error) {
	root, err := os.OpenRoot(s.worldsDir)
	if err != nil {
		return 0, err
	}
	defer root.Close()

	tw := tar.NewWriter(w)
	manifest, err := json.Marshal(snapshotManifest{Files: files})
	if err != nil {
		return 0, err
	}
	if err := writeTarBytes(tw, snapshotManifestName, manifest); err != nil {
		return 0, err
	}

	var sent int64
	for _, f := range files {
		if !needs(f, have) {
			continue
		}
		if err := ctx.Err(); err != nil {
			return sent, err
		}
		if err := copyWorldFile(tw, root, f); err != nil {
			return sent, err
		}
		sent += f.Size
	}
	if err := writeTarBytes(tw, snapshotCompleteName, nil); err != nil {
		return sent, err
	}
	return sent, tw.Close()
}

func writeTarBytes(tw *tar.Writer, name string, body []byte) error {
	if err := tw.WriteHeader(&tar.Header{Name: name, Mode: 0o644, Size: int64(len(body))}); err != nil {
		return err
	}
	_, err := tw.Write(body)
	return err
}

func copyWorldFile(tw *tar.Writer, root *os.Root, f snapshotFile) error {
	src, err := root.Open(f.Name)
	if err != nil {
		return err
	}
	defer src.Close()
	if err := tw.WriteHeader(&tar.Header{Name: f.Name, Mode: 0o644, Size: f.Size}); err != nil {
		return err
	}
	// The server may already be appending past the reported length; only the
	// bytes up to it belong to the saved state.
	if n, err := io.CopyN(tw, src, f.Size); err != nil {
		return fmt.Errorf("%s: copied %d of %d bytes: %w", f.Name, n, f.Size, err)
	}
	return nil
}

// maxSnapshotBodyBytes caps the caller's inventory. A world has hundreds of
// table files at a few dozen bytes each, far under this.
const maxSnapshotBodyBytes = 4 << 20

func (s *server) handleSnapshot(w http.ResponseWriter, r *http.Request) {
	snap := s.snapshots
	// The server-wide write timeout is sized for a console command; a first
	// snapshot streams the whole world, and a refusal can come after the
	// full wait for the save.
	if err := http.NewResponseController(w).SetWriteDeadline(time.Now().Add(snap.readyTimeout + snap.maxHold + 30*time.Second)); err != nil && !errors.Is(err, http.ErrNotSupported) {
		s.logger.Warn("could not extend the snapshot write deadline", "error", err)
	}

	r.Body = http.MaxBytesReader(w, r.Body, maxSnapshotBodyBytes)
	var req snapshotRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid request body", http.StatusBadRequest)
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), snap.maxHold)
	defer cancel()
	defer context.AfterFunc(snap.stop, cancel)()

	files, release, err := snap.hold(ctx)
	if err != nil {
		status, result := http.StatusBadGateway, "failed"
		switch {
		case errors.Is(err, errSnapshotBusy):
			status, result = http.StatusConflict, "busy"
		case errors.Is(err, errHoldActive):
			status, result = http.StatusConflict, "hold_active"
		case errors.Is(err, errNotReady):
			status = http.StatusGatewayTimeout
		case errors.Is(err, errResumeOwed), errors.Is(err, errSnapshotClosed):
			status, result = http.StatusServiceUnavailable, "unavailable"
		}
		metricSnapshots.WithLabelValues(result).Inc()
		s.logger.Warn("snapshot refused", "error", err)
		http.Error(w, err.Error(), status)
		return
	}
	defer release()

	w.Header().Set("Content-Type", "application/x-tar")
	sent, err := snap.stream(ctx, w, files, req.Have)
	metricSnapshotBytes.Add(float64(sent))
	if err != nil {
		// The status line is already sent; the missing completion marker is
		// what tells the caller to discard what it received.
		metricSnapshots.WithLabelValues("failed").Inc()
		s.logger.Error("snapshot stream failed", "error", err, "bytes", sent)
		return
	}
	metricSnapshots.WithLabelValues("ok").Inc()
	s.logger.Info("snapshot served", "files", len(files), "bytes", sent)
}
