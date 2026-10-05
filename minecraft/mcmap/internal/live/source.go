package live

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// Batch is one answer from the bridge.
type Batch struct {
	Records []RawRecord
	// Gap means records were lost between the last poll and this one.
	Gap bool
}

// ErrBusy is the bridge declining to hold another caller waiting.
var ErrBusy = errors.New("the bridge has no free waiter")

// busyError is ErrBusy with how long the bridge asked to be left alone.
type busyError struct{ after time.Duration }

func (e busyError) Error() string { return ErrBusy.Error() }
func (e busyError) Unwrap() error { return ErrBusy }

// Poller is where records come from. Poll returns the records after since,
// waiting up to wait for the first if there are none yet; an empty batch is
// a wait that ran out.
type Poller interface {
	Poll(ctx context.Context, since int64, wait time.Duration) (Batch, error)
}

// maxBatchBytes is several times the bridge's whole ring.
const maxBatchBytes = 8 << 20

// Bridge reads records from the console bridge's long-poll endpoint.
type Bridge struct {
	URL   string
	Token string
	// HTTP may be nil. Its timeout, if any, must be longer than the longest
	// wait asked for.
	HTTP *http.Client
}

// NewBridge builds a Bridge whose client will not follow a redirect: the
// request carries the bridge's token, and wherever a redirect points is not
// the bridge.
func NewBridge(baseURL, token string) *Bridge {
	return &Bridge{URL: baseURL, Token: token, HTTP: &http.Client{
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}}
}

// The bridge sends data as the record's JSON object, with the sentinel and
// any NUL already removed. A string holding the record is read as well, so
// that the two sides need not change in step if that ever does.
type wireBatch struct {
	Records []struct {
		ID   int64           `json:"id"`
		At   time.Time       `json:"at"`
		Data json.RawMessage `json:"data"`
	} `json:"records"`
	Gap bool `json:"gap"`
}

// responseSlack is how long past the wait the bridge gets to answer.
const responseSlack = 10 * time.Second

func (b *Bridge) Poll(ctx context.Context, since int64, wait time.Duration) (Batch, error) {
	ctx, cancel := context.WithTimeout(ctx, wait+responseSlack)
	defer cancel()
	query := url.Values{"since": {strconv.FormatInt(since, 10)}, "wait": {strconv.FormatInt(wait.Milliseconds(), 10)}}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, strings.TrimRight(b.URL, "/")+"/script?"+query.Encode(), nil)
	if err != nil {
		return Batch{}, err
	}
	req.Header.Set("Authorization", "Bearer "+b.Token)
	client := b.HTTP
	if client == nil {
		client = http.DefaultClient
	}
	resp, err := client.Do(req)
	if err != nil {
		return Batch{}, err
	}
	defer resp.Body.Close()
	switch {
	case resp.StatusCode == http.StatusTooManyRequests:
		after, _ := strconv.Atoi(resp.Header.Get("Retry-After"))
		return Batch{}, busyError{after: time.Duration(after) * time.Second}
	case resp.StatusCode != http.StatusOK:
		return Batch{}, fmt.Errorf("bridge answered %s", resp.Status)
	}
	var w wireBatch
	if err := json.NewDecoder(io.LimitReader(resp.Body, maxBatchBytes)).Decode(&w); err != nil {
		return Batch{}, fmt.Errorf("bridge response: %w", err)
	}
	out := Batch{Gap: w.Gap, Records: make([]RawRecord, 0, len(w.Records))}
	for _, r := range w.Records {
		data := string(r.Data)
		if strings.HasPrefix(data, `"`) {
			if err := json.Unmarshal(r.Data, &data); err != nil {
				return Batch{}, fmt.Errorf("bridge response: %w", err)
			}
		}
		out.Records = append(out.Records, RawRecord{ID: r.ID, At: r.At, Data: data})
	}
	return out, nil
}

const (
	retryAfter = time.Second
	// maxRetry is short because nothing is paused or copied by a retry, and
	// every second waited after the bridge returns is a second the map shows
	// nobody.
	maxRetry = 30 * time.Second

	sweepEvery = time.Second
)

// Source keeps a Layer fed from a Poller.
type Source struct {
	Poller Poller
	Layer  *Layer
	// Wait is how long each poll may be held open.
	Wait   time.Duration
	Logger *slog.Logger
	// Now is the clock; nil means time.Now.
	Now func() time.Time

	// retry replaces the first retry delay, for tests.
	retry time.Duration
}

func (s *Source) log() *slog.Logger {
	if s.Logger == nil {
		return slog.New(slog.DiscardHandler)
	}
	return s.Logger
}

// pause waits for d and reports false if ctx ended first.
func pause(ctx context.Context, d time.Duration) bool {
	select {
	case <-ctx.Done():
		return false
	case <-time.After(d):
		return true
	}
}

func (s *Source) now() time.Time {
	if s.Now != nil {
		return s.Now()
	}
	return time.Now()
}

// Run polls until ctx ends. Alongside it a ticker notices lists going
// stale, which by definition happens when no record arrives to prompt it.
func (s *Source) Run(ctx context.Context) {
	done := make(chan struct{})
	go func() {
		defer close(done)
		tick := time.NewTicker(sweepEvery)
		defer tick.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-tick.C:
				s.Layer.Flush(s.now())
			}
		}
	}()
	defer func() { <-done }()

	var since int64
	failures := 0
	for ctx.Err() == nil {
		asked := time.Now()
		batch, err := s.Poller.Poll(ctx, since, s.Wait)
		if ctx.Err() != nil {
			return
		}
		if err != nil {
			failures++
			result := "failed"
			if errors.Is(err, ErrBusy) {
				result = "busy"
			}
			metricPolls.WithLabelValues(result).Inc()
			// One line when it breaks and one when it mends: a bridge that
			// is down would otherwise write this every few seconds.
			if failures == 1 {
				s.log().Warn("live records not read", "error", err.Error())
			}
			delay := s.retryDelay(failures)
			// Being told when to come back is not a fault to back away
			// from: the bridge is up and has said how long it wants.
			if busy, ok := errors.AsType[busyError](err); ok && busy.after > 0 {
				delay = min(maxRetry, busy.after)
			}
			if !pause(ctx, delay) {
				return
			}
			continue
		}
		if failures > 0 {
			s.log().Info("live records read again", "failed_polls", failures)
			failures = 0
		}

		now := s.now()
		switch {
		case batch.Gap:
			metricPolls.WithLabelValues("gap").Inc()
			s.log().Warn("live records were missed: the bridge restarted or had already dropped some", "since", since)
		case len(batch.Records) == 0:
			metricPolls.WithLabelValues("empty").Inc()
		default:
			metricPolls.WithLabelValues("ok").Inc()
		}
		if len(batch.Records) == 0 {
			if batch.Gap {
				since = 0
			}
			// A bridge that is shutting down answers at once with nothing.
			// Asking again immediately would spin until it is gone.
			if time.Since(asked) < s.Wait/2 && !pause(ctx, s.first()) {
				return
			}
			continue
		}
		// Ids restart with the bridge, which then answers with everything
		// it holds, under ids below the cursor it was asked for. So the
		// cursor follows the response and never its own past.
		since = batch.Records[len(batch.Records)-1].ID
		for _, raw := range batch.Records {
			s.Layer.Ingest(raw, now)
		}
		s.Layer.Flush(now)
	}
}

// retryDelay doubles from a second, so a bridge restarting is picked up
// again at once and one that is gone is not asked every second.
func (s *Source) retryDelay(failures int) time.Duration {
	delay := s.first()
	for i := 1; i < failures && delay < maxRetry; i++ {
		delay *= 2
	}
	return min(maxRetry, delay)
}

func (s *Source) first() time.Duration {
	if s.retry > 0 {
		return s.retry
	}
	return retryAfter
}
