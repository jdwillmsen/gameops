package mcauth

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"golang.org/x/oauth2"

	agentauth "github.com/jdwillmsen/gameops/minecraft/agent/pkg/mcauth"
)

// These tests hold afkbot's token cache and the shared agent/pkg/mcauth one
// to the same files until afkbot is deleted. Crew takes over a bot's PVC with
// the token afkbot left there, and a cache file crew cannot find or will not
// load means a device-code prompt and a bot offline until someone signs in.

func TestCompatTokenFileNamesAgree(t *testing.T) {
	for _, username := range []string{
		"AFK-Bot-1",
		"afk_bot_2",
		"  padded  ",
		"Dot.Blaze",
		"../../etc/passwd",
		`back\slash`,
		"with space",
		"Ünïcödé",
		"🤖bot",
		strings.Repeat("LongUserName", 10),
		"   ",
	} {
		t.Run(username, func(t *testing.T) {
			ours, ourErr := tokenFileName(username)
			store, theirErr := agentauth.NewFileStore(t.TempDir(), username)
			if (ourErr == nil) != (theirErr == nil) {
				t.Fatalf("afkbot err = %v, agent err = %v; want both or neither", ourErr, theirErr)
			}
			if ourErr != nil {
				return
			}
			if theirs := filepath.Base(store.Path()); theirs != ours {
				t.Errorf("agent file = %q, afkbot file = %q", theirs, ours)
			}
		})
	}
}

func TestCompatAfkbotTokenLoadsInAgentStore(t *testing.T) {
	dir := t.TempDir()
	store, err := agentauth.NewFileStore(dir, "AFK-Bot-1")
	if err != nil {
		t.Fatal(err)
	}
	want := &oauth2.Token{
		AccessToken:  "access",
		TokenType:    "bearer",
		RefreshToken: "refresh",
		Expiry:       time.Date(2026, 11, 27, 12, 0, 0, 0, time.UTC),
	}
	if err := saveToken(store.Path(), want); err != nil {
		t.Fatal(err)
	}

	got, err := store.Load(context.Background())
	if err != nil {
		t.Fatalf("agent store rejects afkbot's token: %v", err)
	}
	assertSameToken(t, got, want)
}

// The one known gap: oauth2 reads a zero Expiry as "never expires", so the
// agent refuses such a token rather than never refreshing it, while afkbot
// only asks for a refresh token. Pinned exactly so a change on either side is
// noticed before cutover rather than at a bot's next restart.
func TestCompatTokenWithoutExpiryOnlyAfkbotAccepts(t *testing.T) {
	store, err := agentauth.NewFileStore(t.TempDir(), "AFK-Bot-1")
	if err != nil {
		t.Fatal(err)
	}
	if err := saveToken(store.Path(), &oauth2.Token{AccessToken: "access", RefreshToken: "refresh"}); err != nil {
		t.Fatal(err)
	}

	if _, err := loadToken(store.Path()); err != nil {
		t.Errorf("afkbot rejects a token with no expiry: %v", err)
	}
	if _, err := store.Load(context.Background()); err == nil || !strings.Contains(err.Error(), "no expiry") {
		t.Errorf("agent store err = %v, want the no-expiry refusal", err)
	}
}

// Rollback direction: a token crew has rewritten must still start afkbot.
func TestCompatAgentTokenLoadsInAfkbot(t *testing.T) {
	store, err := agentauth.NewFileStore(t.TempDir(), "AFK-Bot-1")
	if err != nil {
		t.Fatal(err)
	}
	want := &oauth2.Token{
		AccessToken:  "access",
		TokenType:    "bearer",
		RefreshToken: "refresh",
		Expiry:       time.Date(2026, 11, 27, 12, 0, 0, 0, time.UTC),
		ExpiresIn:    3600,
	}
	if err := store.Save(context.Background(), want); err != nil {
		t.Fatal(err)
	}

	got, err := loadToken(store.Path())
	if err != nil {
		t.Fatalf("afkbot rejects the agent's token: %v", err)
	}
	assertSameToken(t, got, want)
}

func assertSameToken(t *testing.T, got, want *oauth2.Token) {
	t.Helper()
	if got.AccessToken != want.AccessToken || got.TokenType != want.TokenType ||
		got.RefreshToken != want.RefreshToken || !got.Expiry.Equal(want.Expiry) {
		t.Errorf("token = %+v, want %+v", got, want)
	}
}
