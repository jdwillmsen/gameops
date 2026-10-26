package metrics

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/jdwillmsen/gameops/minecraft/agent/pkg/logging"
	"github.com/jdwillmsen/gameops/minecraft/crew-control/internal/events"
)

func TestEventStoreMetricsSayWhenHistoryIsInMemory(t *testing.T) {
	fb := events.NewFallback(logging.New("error"))
	f := New(nil)
	f.WatchEventStore(fb)
	scrape := func() string {
		rec := httptest.NewRecorder()
		f.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/metrics", nil))
		return rec.Body.String()
	}
	if body := scrape(); !strings.Contains(body, "crew_event_store_degraded 1") {
		t.Fatalf("before the database is reached:\n%s", body)
	}
	ev := []events.Event{{Unit: "crew-01", At: time.Now(), Kind: "online", Severity: events.SevOK}}
	if err := fb.Append(context.Background(), ev); err != nil {
		t.Fatal(err)
	}
	if body := scrape(); !strings.Contains(body, "crew_event_store_diverted_events_total 1") {
		t.Fatalf("diverted events not counted:\n%s", body)
	}
	fb.SetPrimary(events.NewMemory())
	if err := fb.Append(context.Background(), ev); err != nil {
		t.Fatal(err)
	}
	body := scrape()
	if !strings.Contains(body, "crew_event_store_degraded 0") || !strings.Contains(body, "crew_event_store_replayed_events_total 1") {
		t.Fatalf("after recovery:\n%s", body)
	}
}
