package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Made up, like every credential in these tests.
const (
	fakeClientID = "00000000-1111-2222-3333-444444444444"
	fakeSecret   = "a-made-up-client-secret-value-0123456789"
	fakeOrigin   = "https://map.example"
)

func xboxOn(extra ...string) func(string) string {
	return with(append([]string{"XBOX_LOGIN_ENABLED", "true", "XBOX_CLIENT_ID", fakeClientID, "XBOX_CLIENT_SECRET", fakeSecret, "PUBLIC_ORIGIN", fakeOrigin}, extra...)...)
}

func TestLoad_XboxIsOffByDefaultAndNeedsNothingThen(t *testing.T) {
	c, err := Load(with())
	if err != nil {
		t.Fatal(err)
	}
	if c.XboxLogin || c.XboxAllowlist || c.XboxClientID != "" || c.XboxClientSecret != "" {
		t.Errorf("defaults: %+v", c)
	}
	// Set but not enabled: the values are not read at all.
	c, err = Load(with("XBOX_CLIENT_ID", "junk", "XBOX_CLIENT_SECRET", "x"))
	if err != nil || c.XboxLogin || c.XboxClientID != "" {
		t.Errorf("settings while off: %v %+v", err, c)
	}
}

func TestLoad_XboxOn(t *testing.T) {
	c, err := Load(xboxOn("XBOX_ALLOWLIST", "true"))
	if err != nil {
		t.Fatal(err)
	}
	if !c.XboxLogin || !c.XboxAllowlist || c.XboxClientID != fakeClientID || c.XboxClientSecret != fakeSecret || c.PublicOrigin != fakeOrigin {
		t.Errorf("on: %+v", c)
	}
	if c.Admin || c.Passkeys {
		t.Error("Xbox sign-in switched admin or passkeys on")
	}
}

func TestLoad_XboxSecretFromAMountedFileDropsTheTrailingNewline(t *testing.T) {
	path := filepath.Join(t.TempDir(), "secret")
	if err := os.WriteFile(path, []byte(fakeSecret+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	c, err := Load(with("XBOX_LOGIN_ENABLED", "true", "XBOX_CLIENT_ID", fakeClientID, "XBOX_CLIENT_SECRET_FILE", path, "PUBLIC_ORIGIN", fakeOrigin))
	if err != nil || c.XboxClientSecret != fakeSecret {
		t.Fatalf("err %v, secret %q", err, c.XboxClientSecret)
	}
}

func TestLoad_XboxRefusals(t *testing.T) {
	dir := t.TempDir()
	short := filepath.Join(dir, "short")
	_ = os.WriteFile(short, []byte("tiny"), 0o600)
	both := filepath.Join(dir, "both")
	_ = os.WriteFile(both, []byte(fakeSecret), 0o600)
	base := func(over ...string) func(string) string {
		kv := []string{"XBOX_LOGIN_ENABLED", "true", "XBOX_CLIENT_ID", fakeClientID, "XBOX_CLIENT_SECRET", fakeSecret, "PUBLIC_ORIGIN", fakeOrigin}
		return with(append(kv, over...)...) // later pairs win in env
	}
	for name, getenv := range map[string]func(string) string{
		"no client id":            with("XBOX_LOGIN_ENABLED", "true", "XBOX_CLIENT_SECRET", fakeSecret, "PUBLIC_ORIGIN", fakeOrigin),
		"client id not a guid":    base("XBOX_CLIENT_ID", "not-a-guid"),
		"no secret":               with("XBOX_LOGIN_ENABLED", "true", "XBOX_CLIENT_ID", fakeClientID, "PUBLIC_ORIGIN", fakeOrigin),
		"short secret":            base("XBOX_CLIENT_SECRET", "tiny"),
		"secret with space":       base("XBOX_CLIENT_SECRET", "has a space in it, too long to be short"),
		"file and value":          base("XBOX_CLIENT_SECRET_FILE", both),
		"file missing":            with("XBOX_LOGIN_ENABLED", "true", "XBOX_CLIENT_ID", fakeClientID, "XBOX_CLIENT_SECRET_FILE", filepath.Join(dir, "none"), "PUBLIC_ORIGIN", fakeOrigin),
		"file too short":          with("XBOX_LOGIN_ENABLED", "true", "XBOX_CLIENT_ID", fakeClientID, "XBOX_CLIENT_SECRET_FILE", short, "PUBLIC_ORIGIN", fakeOrigin),
		"no origin":               with("XBOX_LOGIN_ENABLED", "true", "XBOX_CLIENT_ID", fakeClientID, "XBOX_CLIENT_SECRET", fakeSecret),
		"origin over http":        base("PUBLIC_ORIGIN", "http://map.example"),
		"origin with port":        base("PUBLIC_ORIGIN", "https://map.example:8443"),
		"not a boolean":           with("XBOX_LOGIN_ENABLED", "sure"),
		"allowlist not a boolean": with("XBOX_ALLOWLIST", "sure"),
	} {
		_, err := Load(getenv)
		if err == nil {
			t.Errorf("%s: want an error", name)
			continue
		}
		if strings.Contains(err.Error(), fakeSecret) || strings.Contains(err.Error(), "tiny") {
			t.Errorf("%s: the error carries the secret: %v", name, err)
		}
	}
}

func TestLoad_XboxWithNoLoginIsTurnedOffNotAnError(t *testing.T) {
	c, err := Load(env("BRIDGE_URL", "http://bridge:8766", "BRIDGE_TOKEN", "tok", "LEVEL_NAME", "FWB", "AUTH_DISABLED", "true", "XBOX_LOGIN_ENABLED", "true"))
	if err != nil || c.XboxLogin {
		t.Errorf("err %v, XboxLogin %v", err, c.XboxLogin)
	}
}
