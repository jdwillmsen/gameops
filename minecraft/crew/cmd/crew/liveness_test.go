package main

import (
	"context"
	"net"
	"net/http"
	"testing"
	"time"

	"github.com/jdwillmsen/gameops/minecraft/agent/pkg/logging"
	"github.com/jdwillmsen/gameops/minecraft/crew/internal/config"
	"golang.org/x/oauth2"
)

func freeAddr(t *testing.T) string {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = l.Close() }()
	return l.Addr().String()
}

func get(t *testing.T, url string) (int, error) {
	t.Helper()
	resp, err := http.Get(url)
	if err != nil {
		return 0, err
	}
	defer func() { _ = resp.Body.Close() }()
	return resp.StatusCode, nil
}

// The kubelet restarts a unit whose /healthz does not answer within the
// liveness window, so a slow start of anything the unit needs from the
// network (sign-in, the control plane) must not hold the status server back.
func TestStatusServerAnswersWhileStartupIsStuckOnTheNetwork(t *testing.T) {
	t.Setenv("MC_HOST", "example.invalid")
	t.Setenv("MC_USERNAME", "crew")
	t.Setenv("CONTROL_URL", "http://crew-control.invalid:8080")
	t.Setenv("CONTROL_TOKEN", "t")
	t.Setenv("STATUS_ADDR", freeAddr(t))
	cfg, err := config.Load()
	if err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	stuck := make(chan struct{})
	open := func(ctx context.Context, _ config.Config) (oauth2.TokenSource, error) {
		close(stuck)
		<-ctx.Done()
		return nil, ctx.Err()
	}
	done := make(chan int)
	go func() { done <- run(ctx, cancel, cfg, open, logging.New("")) }()
	t.Cleanup(func() { cancel(); <-done })

	deadline := time.Now().Add(5 * time.Second)
	for {
		code, err := get(t, "http://"+cfg.StatusAddr+"/healthz")
		if err == nil {
			if code != http.StatusOK {
				t.Fatalf("healthz = %d, want 200", code)
			}
			break
		}
		if time.Now().After(deadline) {
			select {
			case <-stuck:
				t.Fatalf("healthz never answered while sign-in was stuck: %v", err)
			default:
				t.Fatalf("healthz never answered: %v", err)
			}
		}
		time.Sleep(20 * time.Millisecond)
	}

	if code, err := get(t, "http://"+cfg.StatusAddr+"/readyz"); err != nil || code != http.StatusServiceUnavailable {
		t.Errorf("readyz = %d, %v; want 503 until the unit is in the world", code, err)
	}
}
