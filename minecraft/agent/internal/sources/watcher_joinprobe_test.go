package sources

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jdwillmsen/gameops/minecraft/agent/internal/adapters"
	"github.com/jdwillmsen/gameops/minecraft/agent/pkg/logging"
)

// The version announcer went silent for weeks because the exporter it read
// pings over RakNet and the server moved to NetherNet: the exporter kept
// answering, with healthy 0 and no version, so every poll was skipped. This
// drives the real MetricsFacts over a join probe exposition that carries that
// zero reading too, and checks an upgrade is still announced.
func TestWatcherAnnouncesAnUpgradeSeenByTheJoinProbe(t *testing.T) {
	var version atomic.Value
	version.Store("1.26.60")
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprintf(w, "mc_joinprobe_joinable 1\nmc_joinprobe_server_info{version=%q} 1\n", version.Load())
		fmt.Fprintln(w, `minecraft_status_healthy{server_edition="bedrock",server_host="127.0.0.1",server_port="31132",server_version=""} 0`)
	}))
	t.Cleanup(srv.Close)

	facts := adapters.NewMetricsFacts(adapters.NewMetricsClient(srv.URL, "", time.Second), nil)
	pub := &recordingPublisher{}
	w := NewWatcher(facts, pub, logging.New("error"))

	w.Poll(context.Background())
	version.Store("1.26.70")
	w.Poll(context.Background())

	got := pub.all()
	if len(got) != 1 || !strings.Contains(got[0].Body, "Bedrock 1.26.70") {
		t.Fatalf("announcements = %+v, want one naming Bedrock 1.26.70", got)
	}
}
