package adapters

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// The expositions below are copied verbatim from the live exporters, not
// invented. Every field these commands read -- the version label, the
// /metrics.txt path, the zero-vs-absent distinction -- was wrong in an earlier
// draft written from assumption, so the fixtures are real output.
//
// liveJoinProbe is the FWB join probe on 2026-11-11, with the server on
// NetherNet and healthy.
const liveJoinProbe = `mc_joinprobe_attempts_total{stage="handshake"} 0
mc_joinprobe_attempts_total{stage="pong"} 119
mc_joinprobe_attempts_total{stage="refused"} 0
mc_joinprobe_attempts_total{stage="unreachable"} 0
mc_joinprobe_dialed_protocol 2225
mc_joinprobe_duration_seconds 0.000684761
mc_joinprobe_joinable 1
mc_joinprobe_last_joinable_timestamp_seconds 1.794383344e+09
mc_joinprobe_play_status -1
mc_joinprobe_server_info{version="1.26.60"} 1
mc_joinprobe_server_protocol 2225
mc_joinprobe_stage 1
`

// liveRakNetExporter is the mc-monitor exporter the same minute: it pings
// over RakNet only, so against a NetherNet server it reads unhealthy with no
// version. The agent used to read this and tell players the server was down.
const liveRakNetExporter = `minecraft_status_healthy{server_edition="bedrock",server_host="127.0.0.1",server_port="31132",server_version=""} 0
`

const liveBackup = `backup_last_success_timestamp_seconds{backup_job="jdwillmsen-minecraft-fwb-prd-backup",artifact="world"} 1788819879
backup_last_artifact_bytes{backup_job="jdwillmsen-minecraft-fwb-prd-backup",artifact="world"} 398258140
backup_max_age_seconds{backup_job="jdwillmsen-minecraft-fwb-prd-backup",artifact="world"} 172800
backup_last_artifact_quiesced{backup_job="jdwillmsen-minecraft-fwb-prd-backup",artifact="world"} 1
`

func serve(t *testing.T, body string) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(srv.Close)
	return srv
}

// counts is a PlayerCounter with a fixed answer.
type counts struct {
	online, max int
	err         error
}

func (c counts) PlayerCounts(context.Context) (int, int, error) { return c.online, c.max, c.err }

func factsFor(t *testing.T, probeURL, backupURL string, now time.Time) *MetricsFacts {
	t.Helper()
	return factsWithPlayers(t, probeURL, backupURL, nil, now)
}

func factsWithPlayers(t *testing.T, probeURL, backupURL string, players PlayerCounter, now time.Time) *MetricsFacts {
	t.Helper()
	f := NewMetricsFacts(NewMetricsClient(probeURL, backupURL, 2*time.Second), players)
	f.now = func() time.Time { return now }
	return f
}

func failing(t *testing.T) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "down", http.StatusServiceUnavailable)
	}))
	t.Cleanup(srv.Close)
	return srv
}

func TestServerStatusReportsCountsHealthAndVersion(t *testing.T) {
	f := factsWithPlayers(t, serve(t, liveJoinProbe).URL, "", counts{online: 5, max: 20}, time.Now())
	got, err := f.ServerStatus(context.Background())
	if err != nil {
		t.Fatalf("ServerStatus: %v", err)
	}
	if want := "5/20 players online, server accepting joins, running 1.26.60."; got != want {
		t.Errorf("ServerStatus = %q, want %q", got, want)
	}
}

// The outage this replaced: a NetherNet server, healthy, with the RakNet-only
// exporter reading 0. Whatever that exporter says, !online must neither call
// the server down nor claim staff were alerted -- and an agent still pointed
// at the exporter has nothing it can read, so it says the status is
// unavailable rather than inventing an outage.
func TestServerStatusIsNotFooledByTheRakNetExporterReadingZero(t *testing.T) {
	for name, body := range map[string]string{
		"join probe":            liveJoinProbe + liveRakNetExporter,
		"exporter in its place": liveRakNetExporter,
	} {
		t.Run(name, func(t *testing.T) {
			f := factsWithPlayers(t, serve(t, body).URL, "", counts{online: 2, max: 20}, time.Now())
			got, err := f.ServerStatus(context.Background())
			if err != nil {
				t.Fatalf("ServerStatus: %v", err)
			}
			for _, bad := range []string{"NOT responding", "alerted"} {
				if strings.Contains(got, bad) {
					t.Errorf("ServerStatus = %q, must not say %q", got, bad)
				}
			}
			if !strings.HasPrefix(got, "2/20 players online, ") {
				t.Errorf("ServerStatus = %q, want the console's count first", got)
			}
		})
	}
	got, _ := factsWithPlayers(t, serve(t, liveRakNetExporter).URL, "", nil, time.Now()).ServerStatus(context.Background())
	if got != "Server status unavailable." {
		t.Errorf("ServerStatus over the exporter alone = %q, want it to say the status is unavailable", got)
	}
}

// A failed join check is said plainly, but as what it is: the probe's last
// attempt, not a page to staff that this process cannot know was sent.
func TestServerStatusSaysSoWhenTheJoinCheckFails(t *testing.T) {
	body := strings.Replace(liveJoinProbe, "mc_joinprobe_joinable 1", "mc_joinprobe_joinable 0", 1)
	got, err := factsFor(t, serve(t, body).URL, "", time.Now()).ServerStatus(context.Background())
	if err != nil {
		t.Fatalf("ServerStatus: %v", err)
	}
	if !strings.Contains(got, "did not answer its last join check") || strings.Contains(got, "alerted") {
		t.Errorf("ServerStatus = %q, want a failed join check without a claim about staff", got)
	}
}

// A pinned-protocol probe that the server answered and turned away is a
// version skew, and must not read as the server being unreachable.
func TestServerStatusSaysRefusedWhenTheServerTurnedTheProbeAway(t *testing.T) {
	body := strings.NewReplacer("mc_joinprobe_joinable 1", "mc_joinprobe_joinable 0", "mc_joinprobe_stage 1", "mc_joinprobe_stage 2").Replace(liveJoinProbe)
	got, err := factsFor(t, serve(t, body).URL, "", time.Now()).ServerStatus(context.Background())
	if err != nil {
		t.Fatalf("ServerStatus: %v", err)
	}
	if !strings.Contains(got, "refused its last join check") {
		t.Errorf("ServerStatus = %q, want the refusal named", got)
	}
}

// Each source degrades on its own: a probe that cannot be scraped costs the
// health half, a console that cannot be asked costs the count, and neither
// turns into an error the player sees as the command breaking.
func TestServerStatusDegradesSourceBySource(t *testing.T) {
	cases := []struct {
		name    string
		probe   string
		players PlayerCounter
		want    string
	}{
		{"probe down", failing(t).URL, counts{online: 3, max: 20}, "3/20 players online, server status unavailable."},
		{"console down", serve(t, liveJoinProbe).URL, counts{err: errors.New("bridge timeout")}, "Server accepting joins, running 1.26.60."},
		{"both down", failing(t).URL, counts{err: errors.New("bridge timeout")}, "Server status unavailable."},
		{"no probe configured", "", counts{online: 0, max: 20}, "0/20 players online, server status unavailable."},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := factsWithPlayers(t, tc.probe, "", tc.players, time.Now()).ServerStatus(context.Background())
			if err != nil {
				t.Fatalf("ServerStatus: %v", err)
			}
			if got != tc.want {
				t.Errorf("ServerStatus = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestVersionReadsTheLabelNotAValue(t *testing.T) {
	got, err := factsFor(t, serve(t, liveJoinProbe+liveRakNetExporter).URL, "", time.Now()).Version(context.Background())
	if err != nil {
		t.Fatalf("Version: %v", err)
	}
	if got != "Bedrock 1.26.60." {
		t.Errorf("Version = %q, want %q", got, "Bedrock 1.26.60.")
	}
}

// Before its first answer the probe publishes no server_info at all; that is
// "no version yet", which a watcher must skip rather than compare against.
func TestCurrentVersionRefusesBeforeTheProbeHasSeenOne(t *testing.T) {
	body := strings.Replace(liveJoinProbe, `mc_joinprobe_server_info{version="1.26.60"} 1`+"\n", "", 1)
	if _, err := factsFor(t, serve(t, body).URL, "", time.Now()).CurrentVersion(context.Background()); err == nil {
		t.Fatal("CurrentVersion: want an error with no server_info series, got none")
	}
}

func TestBackupStatusReportsAgeSizeAndQuiesced(t *testing.T) {
	// Two hours after the fixture's backup timestamp.
	now := time.Unix(1788819879, 0).Add(2 * time.Hour)
	got, err := factsFor(t, "", serve(t, liveBackup).URL, now).BackupStatus(context.Background())
	if err != nil {
		t.Fatalf("BackupStatus: %v", err)
	}
	for _, want := range []string{"2 hours ago", "379.8 MB", "clean"} {
		if !strings.Contains(got, want) {
			t.Errorf("BackupStatus = %q, missing %q", got, want)
		}
	}
	if strings.Contains(got, "WARNING") {
		t.Errorf("BackupStatus = %q, warned about a backup well inside policy", got)
	}
}

// backup_max_age_seconds is policy, not decoration: a backup older than it
// has to be called out, because the whole point of asking is "how much could
// I lose".
func TestBackupStatusWarnsWhenOlderThanPolicy(t *testing.T) {
	now := time.Unix(1788819879, 0).Add(72 * time.Hour) // policy is 48h
	got, err := factsFor(t, "", serve(t, liveBackup).URL, now).BackupStatus(context.Background())
	if err != nil {
		t.Fatalf("BackupStatus: %v", err)
	}
	if !strings.Contains(got, "WARNING") {
		t.Errorf("BackupStatus = %q, want a policy warning at 72h against a 48h limit", got)
	}
}

// The exporter publishes zeros before the first real run. Reporting an age
// derived from the epoch would be arithmetic on a placeholder.
func TestBackupStatusTreatsZeroTimestampAsNeverRun(t *testing.T) {
	body := strings.Replace(liveBackup, "} 1788819879", "} 0", 1)
	got, err := factsFor(t, "", serve(t, body).URL, time.Now()).BackupStatus(context.Background())
	if err != nil {
		t.Fatalf("BackupStatus: %v", err)
	}
	if !strings.Contains(got, "No backup has completed yet") {
		t.Errorf("BackupStatus = %q, want a never-run message", got)
	}
}

// A wrong path returns a 404 HTML body that parses as zero metrics. Without
// the status check that reads as "the backup never ran" rather than
// "you are asking the wrong URL" -- the failure this test exists to prevent.
func TestWrongPathFailsLoudlyRatherThanLookingEmpty(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.NotFound(w, r)
	}))
	t.Cleanup(srv.Close)
	_, err := factsFor(t, "", srv.URL, time.Now()).BackupStatus(context.Background())
	if err == nil {
		t.Fatal("BackupStatus: want an error for a 404 endpoint, got none")
	}
	if !strings.Contains(err.Error(), "404") {
		t.Errorf("error = %v, want it to name the status code", err)
	}
}

// An agent deployed without the exporters is degraded, not broken.
func TestUnconfiguredEndpointsReportThemselves(t *testing.T) {
	f := factsFor(t, "", "", time.Now())
	for name, call := range map[string]func() (string, error){
		"ServerStatus": func() (string, error) { return f.ServerStatus(context.Background()) },
		"Version":      func() (string, error) { return f.Version(context.Background()) },
		"BackupStatus": func() (string, error) { return f.BackupStatus(context.Background()) },
	} {
		got, err := call()
		if err != nil {
			t.Errorf("%s: unexpected error %v", name, err)
		}
		if !strings.Contains(got, "isn't configured") {
			t.Errorf("%s = %q, want an unconfigured message", name, got)
		}
	}
}

// Label values may contain commas; splitting naively would corrupt the
// version this reads.
func TestLabelParsingSurvivesCommasInValues(t *testing.T) {
	body := `mc_joinprobe_server_info{note="a,b",version="1.26.60"} 1` + "\n"
	got, err := factsFor(t, serve(t, body).URL, "", time.Now()).Version(context.Background())
	if err != nil {
		t.Fatalf("Version: %v", err)
	}
	if got != "Bedrock 1.26.60." {
		t.Errorf("Version = %q, want the version intact past a comma-bearing label", got)
	}
}
