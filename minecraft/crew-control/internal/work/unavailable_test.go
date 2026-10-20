package work

import (
	"errors"
	"net"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

// A pool aimed at a closed port stands in for a database that went away after
// start; creating it does not dial.
func deadPostgres(t *testing.T) *Postgres {
	t.Helper()
	pool, err := pgxpool.New(ctx, "postgres://u:p@127.0.0.1:1/db?sslmode=disable&connect_timeout=1")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	return &Postgres{pool: pool}
}

func TestPostgresDownIsUnavailableNotAnError(t *testing.T) {
	p := deadPostgres(t)
	if _, _, _, err := p.Next(ctx, "crew-01", "", time.Time{}); !errors.Is(err, ErrUnavailable) {
		t.Errorf("Next: %v", err)
	}
	if _, err := p.Apply(ctx, "crew-01", uuid.New(), Running, 0, ""); !errors.Is(err, ErrUnavailable) {
		t.Errorf("Apply: %v", err)
	}
	if _, err := p.Recent(ctx, "crew-01", 1); !errors.Is(err, ErrUnavailable) {
		t.Errorf("Recent: %v", err)
	}
	if _, err := p.Prune(ctx, time.Time{}); !errors.Is(err, ErrUnavailable) {
		t.Errorf("Prune: %v", err)
	}
}

func TestClosedPoolIsUnavailable(t *testing.T) {
	p := deadPostgres(t)
	p.Close()
	if _, err := p.Recent(ctx, "crew-01", 1); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("Recent: %v", err)
	}
}

func TestWorkRoutesAnswer503WhenPostgresIsDown(t *testing.T) {
	r := newRig(t, deadPostgres(t))
	if resp := r.do(t, "GET", "/v1/units/crew-01/work?wait=1s", "", nil); resp.StatusCode != 503 {
		t.Fatalf("work: %d", resp.StatusCode)
	}
	body := `{"id":"` + uuid.NewString() + `","state":"running"}`
	if resp := r.do(t, "POST", "/v1/units/crew-01/events", body, nil); resp.StatusCode != 503 {
		t.Fatalf("events: %d", resp.StatusCode)
	}
}

// A paused database accepts connections and never answers, so nothing fails
// on its own: only a deadline turns the wait into "unavailable".
func hungPostgres(t *testing.T) *Postgres {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = ln.Close() })
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			t.Cleanup(func() { _ = c.Close() })
		}
	}()
	pool, err := pgxpool.New(ctx, "postgres://u:p@"+ln.Addr().String()+"/db?sslmode=disable")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	return &Postgres{pool: pool}
}

func TestHungPostgresIsUnavailable(t *testing.T) {
	defer func(d time.Duration) { queryTimeout = d }(queryTimeout)
	queryTimeout = 200 * time.Millisecond
	p := hungPostgres(t)
	done := make(chan error, 1)
	go func() {
		_, err := p.Recent(ctx, "crew-01", 1)
		done <- err
	}()
	select {
	case err := <-done:
		if !errors.Is(err, ErrUnavailable) {
			t.Fatalf("Recent: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Recent still waiting on a database that never answers")
	}
	if _, _, _, err := p.Next(ctx, "crew-01", "", time.Time{}); !errors.Is(err, ErrUnavailable) {
		t.Errorf("Next: %v", err)
	}
}
