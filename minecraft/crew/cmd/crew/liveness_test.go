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
	resp, err := (&http.Client{Timeout: time.Second}).Get(url)
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
	t.Cleanup(func() {
		cancel()
		select {
		case <-done:
		case <-time.After(5 * time.Second):
			t.Error("run did not return after cancel")
		}
	})

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

	select {
	case <-stuck:
	case <-time.After(5 * time.Second):
		t.Fatal("startup never reached sign-in")
	}
	if code, err := get(t, "http://"+cfg.StatusAddr+"/healthz"); err != nil || code != http.StatusOK {
		t.Errorf("healthz = %d, %v while sign-in is stuck; want 200", code, err)
	}
	if code, err := get(t, "http://"+cfg.StatusAddr+"/readyz"); err != nil || code != http.StatusServiceUnavailable {
		t.Errorf("readyz = %d, %v; want 503 until the unit is in the world", code, err)
	}
}

// A unit that cannot bind its status address is unobservable to the kubelet,
// so it must fail the process rather than finish "successfully".
func TestRunFailsWhenTheStatusAddressIsTaken(t *testing.T) {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = l.Close() }()
	t.Setenv("MC_HOST", "example.invalid")
	t.Setenv("MC_USERNAME", "crew")
	t.Setenv("STATUS_ADDR", l.Addr().String())
	cfg, err := config.Load()
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	open := func(ctx context.Context, _ config.Config) (oauth2.TokenSource, error) {
		<-ctx.Done()
		return nil, ctx.Err()
	}
	done := make(chan int, 1)
	go func() { done <- run(ctx, cancel, cfg, open, logging.New("")) }()
	select {
	case code := <-done:
		if code != 1 {
			t.Errorf("exit code = %d, want 1", code)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("run did not return after the status listener failed")
	}
}
