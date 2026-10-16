package main

import (
	"context"
	"errors"
	"testing"

	"github.com/jdwillmsen/gameops/minecraft/crew/internal/config"
	"golang.org/x/oauth2"
)

type fakeSource struct{}

func (fakeSource) Token() (*oauth2.Token, error) { return &oauth2.Token{}, nil }

func TestOfflineAuthSkipsTheTokenStore(t *testing.T) {
	opened := false
	open := func(context.Context, config.Config) (oauth2.TokenSource, error) {
		opened = true
		return fakeSource{}, nil
	}
	a, err := resolveAuth(context.Background(), config.Config{Offline: true, Username: "crew-1"}, open)
	if err != nil {
		t.Fatalf("resolveAuth: %v", err)
	}
	if opened {
		t.Error("the token store was opened in offline mode")
	}
	if a.tokens != nil {
		t.Error("offline auth carries a token source")
	}
	if a.identity.DisplayName != "crew-1" {
		t.Errorf("DisplayName = %q, want crew-1", a.identity.DisplayName)
	}
}

func TestDefaultAuthUsesTheTokenSource(t *testing.T) {
	open := func(context.Context, config.Config) (oauth2.TokenSource, error) { return fakeSource{}, nil }
	a, err := resolveAuth(context.Background(), config.Config{Username: "crew-1"}, open)
	if err != nil {
		t.Fatalf("resolveAuth: %v", err)
	}
	if a.tokens == nil {
		t.Error("signed-in auth has no token source")
	}
	if a.identity.DisplayName != "" {
		t.Errorf("DisplayName = %q; a signed-in unit takes its name from the token", a.identity.DisplayName)
	}
}

func TestDefaultAuthReportsStoreFailure(t *testing.T) {
	want := errors.New("boom")
	open := func(context.Context, config.Config) (oauth2.TokenSource, error) { return nil, want }
	if _, err := resolveAuth(context.Background(), config.Config{}, open); !errors.Is(err, want) {
		t.Fatalf("err = %v, want %v", err, want)
	}
}
