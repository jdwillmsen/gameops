package adapters

import (
	"context"
	"errors"
	"fmt"
	"math"
	"strings"
	"time"
)

// MetricsFacts answers the server-information commands that the console
// cannot: !online, !version and !backup.
type MetricsFacts struct {
	client *MetricsClient
	// players is the console's own count for !online. Optional: the server
	// watcher reads only versions and backups and has no use for it.
	players PlayerCounter
	// now is injected so the age arithmetic in BackupStatus is testable
	// without freezing the process clock.
	now func() time.Time
}

// PlayerCounter reads how many players are online and how many the server
// allows. Satisfied by BridgeClient.
type PlayerCounter interface {
	PlayerCounts(ctx context.Context) (online, max int, err error)
}

// NewMetricsFacts builds a MetricsFacts over client. players may be nil, in
// which case !online leaves the count out.
func NewMetricsFacts(client *MetricsClient, players PlayerCounter) *MetricsFacts {
	return &MetricsFacts{client: client, players: players, now: time.Now}
}

const (
	notConfigured     = "That command isn't configured on this server."
	statusUnavailable = "server status unavailable"
)

// StatusEnabled reports whether the join probe is configured; it backs both
// ServerStatus and Version, which read the same exposition.
func (f *MetricsFacts) StatusEnabled() bool { return f.client != nil && f.client.joinProbeURL != "" }

// BackupEnabled reports whether the backup exporter is configured.
func (f *MetricsFacts) BackupEnabled() bool { return f.client != nil && f.client.backupURL != "" }

// ServerStatus answers !online: how many players, whether the server is
// letting clients in, and which build it runs.
//
// Deliberately more than a count. !players already lists names, so a second
// command repeating them earns nothing; what it cannot tell you is whether
// the server is healthy, which is the other half of "is the server ok" that
// players actually mean when they ask.
//
// Each half degrades on its own. A reading that cannot be had is reported as
// unavailable rather than as the server being down: a player told the server
// is broken when only a scrape failed goes looking for a problem that is not
// there.
func (f *MetricsFacts) ServerStatus(ctx context.Context) (string, error) {
	if !f.StatusEnabled() && f.players == nil {
		return notConfigured, nil
	}

	// Concurrent, so a console slow to answer `list` cannot spend the
	// command's deadline before the probe has been read.
	type count struct {
		online, max int
		err         error
	}
	counted := make(chan count, 1)
	if f.players != nil {
		go func() {
			online, max, err := f.players.PlayerCounts(ctx)
			counted <- count{online, max, err}
		}()
	}
	probe, err := f.probe(ctx)

	var parts []string
	if f.players != nil {
		if c := <-counted; c.err == nil {
			parts = append(parts, fmt.Sprintf("%d/%d players online", c.online, c.max))
		} else {
			parts = append(parts, "player count unavailable")
		}
	}

	joinable, hasJoinable := first(probe, "mc_joinprobe_joinable")
	passing := err == nil && hasJoinable && joinable.value == 1
	switch {
	case err != nil || !hasJoinable:
		parts = append(parts, statusUnavailable)
	case passing:
		// "Passing", not "accepting joins": over NetherNet the probe reaches
		// the signaling endpoint and stops before login, so a login fault
		// further on would still pass it.
		parts = append(parts, "server passing its join check")
	case stageRefused(probe):
		// It answered and turned the session away, which is a version skew
		// rather than an outage; "did not answer" would send players looking
		// for the wrong fault.
		parts = append(parts, "server refused its last join check")
	default:
		// The probe's last attempt only. One miss is often a restart in
		// progress, and the chart's own alert waits out a window before it
		// calls anyone, so this does not claim that staff were told.
		parts = append(parts, "server did not answer its last join check")
	}
	// The probe keeps the last version it saw through a failed attempt, so
	// one read while it is failing is history, not what is running now.
	if v := serverVersion(probe); v != "" {
		if passing {
			parts = append(parts, "running "+v)
		} else {
			parts = append(parts, "last seen running "+v)
		}
	}

	if len(parts) == 0 {
		return "Server status unavailable.", nil
	}
	msg := strings.Join(parts, ", ") + "."
	return strings.ToUpper(msg[:1]) + msg[1:], nil
}

// stageRefused reports whether the probe's last attempt reached the server
// and was refused a session, mc_joinprobe_stage's 2.
func stageRefused(probe map[string][]sample) bool {
	s, ok := first(probe, "mc_joinprobe_stage")
	return ok && s.value == 2
}

// probe reads the join probe, or nothing when it is not configured.
func (f *MetricsFacts) probe(ctx context.Context) (map[string][]sample, error) {
	if !f.StatusEnabled() {
		return nil, errStatusUnconfigured
	}
	m, err := f.client.fetch(ctx, f.client.joinProbeURL)
	if err != nil {
		return nil, fmt.Errorf("metrics facts: join probe: %w", err)
	}
	return m, nil
}

// Version answers !version with the server build the join probe last saw.
//
// The version is a label on mc_joinprobe_server_info rather than a metric
// value of its own, which is why this reads a label instead of a number.
func (f *MetricsFacts) Version(ctx context.Context) (string, error) {
	if !f.StatusEnabled() {
		return notConfigured, nil
	}
	v, err := f.CurrentVersion(ctx)
	if err != nil {
		return "", err
	}
	return "Bedrock " + v + ".", nil
}

// errStatusUnconfigured and errBackupUnconfigured are what the raw readings
// return without their exporter. An error rather than the player-facing
// notConfigured line: the caller of a raw reading is a watcher comparing
// values, and a sentence is not a version it should ever compare against.
var (
	errStatusUnconfigured = errors.New("metrics facts: the join probe is not configured")
	errBackupUnconfigured = errors.New("metrics facts: backup exporter is not configured")
)

// CurrentVersion is the bare version label, for a caller that compares
// versions rather than reading one out to a player.
func (f *MetricsFacts) CurrentVersion(ctx context.Context) (string, error) {
	m, err := f.probe(ctx)
	if err != nil {
		return "", fmt.Errorf("metrics facts: version: %w", err)
	}
	v := serverVersion(m)
	if v == "" {
		return "", fmt.Errorf("metrics facts: version: the join probe has not seen a server version yet")
	}
	return v, nil
}

// BackupState is the backup exporter's reading, reduced to the facts a
// staleness check needs.
type BackupState struct {
	// Completed is false while the exporter still publishes its placeholder
	// zero: there is no backup yet, so there is no age to be stale by.
	Completed bool
	Age       time.Duration
	// MaxAge is zero when the exporter publishes no policy, in which case
	// nothing is stale because nothing says what stale means.
	MaxAge time.Duration
}

// Stale reports whether the last backup is older than the exporter's own
// policy allows. The same rule !backup's warning uses, defined once so a
// warning in chat and a notice to operators can never disagree.
func (b BackupState) Stale() bool {
	return b.Completed && b.MaxAge > 0 && b.Age > b.MaxAge
}

// BackupState reads the backup exporter for a caller that decides what to
// do about its age, rather than one reading it out to a player.
func (f *MetricsFacts) BackupState(ctx context.Context) (BackupState, error) {
	if !f.BackupEnabled() {
		return BackupState{}, errBackupUnconfigured
	}
	m, err := f.client.fetch(ctx, f.client.backupURL)
	if err != nil {
		return BackupState{}, fmt.Errorf("metrics facts: backup state: %w", err)
	}
	return f.backupState(m)
}

func (f *MetricsFacts) backupState(m map[string][]sample) (BackupState, error) {
	ts, ok := first(m, "backup_last_success_timestamp_seconds")
	if !ok {
		return BackupState{}, fmt.Errorf("metrics facts: backup status: exporter published no backup_last_success_timestamp_seconds")
	}
	if ts.value <= 0 {
		return BackupState{}, nil
	}
	st := BackupState{Completed: true, Age: f.now().Sub(time.Unix(int64(ts.value), 0))}
	if maxAge, ok := first(m, "backup_max_age_seconds"); ok && maxAge.value > 0 {
		st.MaxAge = time.Duration(maxAge.value) * time.Second
	}
	return st, nil
}

// BackupStatus answers !backup: when the world was last saved, how big the
// archive was, and whether it was taken cleanly.
//
// Age is reported rather than a timestamp because a timestamp requires the
// reader to know the server's timezone and do arithmetic in their head, and
// the question behind !backup is almost always "how much could I lose".
func (f *MetricsFacts) BackupStatus(ctx context.Context) (string, error) {
	if f.client.backupURL == "" {
		return notConfigured, nil
	}
	m, err := f.client.fetch(ctx, f.client.backupURL)
	if err != nil {
		return "", fmt.Errorf("metrics facts: backup status: %w", err)
	}

	st, err := f.backupState(m)
	if err != nil {
		return "", err
	}
	if !st.Completed {
		// The exporter publishes zeros as placeholders before the first real
		// run. Reporting "56 years ago" would be technically derived from the
		// data and useless.
		return "No backup has completed yet.", nil
	}

	parts := []string{"Last world backup " + formatAge(st.Age) + " ago"}

	if size, ok := first(m, "backup_last_artifact_bytes"); ok && size.value > 0 {
		parts = append(parts, formatBytes(size.value))
	}
	if q, ok := first(m, "backup_last_artifact_quiesced"); ok {
		if q.value == 1 {
			parts = append(parts, "taken with the world held (clean)")
		} else {
			// Surfaced rather than hidden: an unquiesced copy is still
			// restorable, but it is not the same guarantee, and a player
			// asking about backups is asking exactly this.
			parts = append(parts, "taken without holding the world (still restorable)")
		}
	}

	msg := strings.Join(parts, ", ") + "."
	if st.Stale() {
		msg += " WARNING: that is older than this server's backup policy allows."
	}
	return msg, nil
}

// serverVersion reads the version label off mc_joinprobe_server_info. The
// probe resets the series before setting a new one, so at most one is
// published; the value check guards against one ever left at 0.
func serverVersion(m map[string][]sample) string {
	for _, s := range m["mc_joinprobe_server_info"] {
		if v := s.labels["version"]; v != "" && s.value == 1 {
			return v
		}
	}
	return ""
}

// formatAge renders a duration the way someone reads it aloud, because these
// strings go to players in chat rather than into a log.
func formatAge(d time.Duration) string {
	if d < 0 {
		d = 0
	}
	switch {
	case d < time.Minute:
		return "less than a minute"
	case d < time.Hour:
		return pluralise(int(d.Minutes()), "minute")
	case d < 24*time.Hour:
		return pluralise(int(d.Hours()), "hour")
	default:
		return pluralise(int(d.Hours()/24), "day")
	}
}

func pluralise(n int, unit string) string {
	if n == 1 {
		return fmt.Sprintf("1 %s", unit)
	}
	return fmt.Sprintf("%d %ss", n, unit)
}

func formatBytes(b float64) string {
	const unit = 1024.0
	if b < unit {
		return fmt.Sprintf("%.0f B", b)
	}
	exp := int(math.Log(b) / math.Log(unit))
	if exp > 4 {
		exp = 4
	}
	return fmt.Sprintf("%.1f %cB", b/math.Pow(unit, float64(exp)), "KMGTP"[exp-1])
}
