package main

import (
	"context"
	"os"

	"github.com/jdwillmsen/gameops/minecraft/agent/pkg/mcauth"
	"github.com/jdwillmsen/gameops/minecraft/crew/internal/config"
	"github.com/sandertv/gophertunnel/minecraft/protocol/login"
	"golang.org/x/oauth2"
)

// authn is what the dialer needs to identify the unit: a token source for a
// signed-in unit, or a bare identity for an offline one. Never both.
type authn struct {
	tokens   oauth2.TokenSource
	identity login.IdentityData
}

type tokenOpener func(context.Context, config.Config) (oauth2.TokenSource, error)

// resolveAuth takes the opener as a parameter so offline mode can be shown to
// never reach the token store.
func resolveAuth(ctx context.Context, cfg config.Config, open tokenOpener) (authn, error) {
	if cfg.Offline {
		return authn{identity: login.IdentityData{DisplayName: cfg.Username}}, nil
	}
	ts, err := open(ctx, cfg)
	if err != nil {
		return authn{}, err
	}
	return authn{tokens: ts}, nil
}

func openTokenSource(ctx context.Context, cfg config.Config) (oauth2.TokenSource, error) {
	store, err := mcauth.NewFileStore(cfg.AuthCacheDir, cfg.Username)
	if err != nil {
		return nil, err
	}
	// The device code is printed by design: a first start against an empty
	// cache needs a human, and this is the only place the code appears.
	return mcauth.TokenSource(ctx, store, os.Stdout)
}
