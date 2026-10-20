package telemetry

import (
	"context"
	"net/http"
)

// JobsSource renders the jobs event payload. Like WorkSource it is handed in
// by main so that only signed-in streams can read it.
type JobsSource func(ctx context.Context) ([]byte, error)

func (p *Poller) SetJobsSource(src JobsSource) {
	p.mu.Lock()
	p.jobsSrc = src
	p.mu.Unlock()
}

func (p *Poller) jobsSource() JobsSource {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.jobsSrc
}

// PublishJobs tells every signed-in stream that jobs changed. The payload is
// rendered when a stream writes, from the store as it is then, so a burst of
// changes collapses into one event with the latest list.
func (p *Poller) PublishJobs() {
	p.mu.Lock()
	defer p.mu.Unlock()
	for c := range p.jsubs {
		select {
		case c <- struct{}{}:
		default:
		}
	}
}

func (p *Poller) subscribeJobs() (<-chan struct{}, func()) {
	c := make(chan struct{}, 1)
	p.mu.Lock()
	p.jsubs[c] = struct{}{}
	p.mu.Unlock()
	return c, func() {
		p.mu.Lock()
		delete(p.jsubs, c)
		p.mu.Unlock()
	}
}

// writeJobs skips a list that cannot be read (the store is down) rather than
// ending the stream: the units event does not depend on it.
func (p *Poller) writeJobs(w http.ResponseWriter, rc *http.ResponseController, r *http.Request) bool {
	src := p.jobsSource()
	if src == nil {
		return true
	}
	data, err := src(r.Context())
	if err != nil {
		return true
	}
	return writeEvent(w, rc, "jobs", data)
}
