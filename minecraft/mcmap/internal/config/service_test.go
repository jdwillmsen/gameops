package config

import (
	"strings"
	"testing"
	"time"
)

// Made up, like every credential in these tests.
const serviceSecret = "a-made-up-service-secret-0123456789abcdef"

func TestLoad_ServiceSessionsAreOffUnlessASecretIsGiven(t *testing.T) {
	c, err := Load(with())
	if err != nil {
		t.Fatal(err)
	}
	if c.ServiceSecret != "" {
		t.Errorf("a service secret of %d characters came from nowhere", len(c.ServiceSecret))
	}
	// The lifetime alone switches nothing on, and is not even read.
	if c, err = Load(with("SERVICE_SESSION_TTL", "not a duration")); err != nil || c.ServiceSecret != "" {
		t.Errorf("a lifetime with no secret: %v", err)
	}

	c, err = Load(with("SERVICE_SESSION_SECRET", serviceSecret))
	if err != nil {
		t.Fatal(err)
	}
	if c.ServiceSecret != serviceSecret || c.ServiceSessionTTL != 10*time.Minute {
		t.Errorf("with a secret: lifetime %s", c.ServiceSessionTTL)
	}
	if c, err = Load(with("SERVICE_SESSION_SECRET", serviceSecret, "SERVICE_SESSION_TTL", "3m")); err != nil || c.ServiceSessionTTL != 3*time.Minute {
		t.Errorf("SERVICE_SESSION_TTL=3m gave %s, %v", c.ServiceSessionTTL, err)
	}
}

func TestLoad_AServiceSecretThatWouldWeakenTheLoginStopsTheService(t *testing.T) {
	for name, extra := range map[string][]string{
		"short":                         {"SERVICE_SESSION_SECRET", serviceSecret[:31]},
		"the agent's token":             {"SERVICE_SESSION_SECRET", "internal-token-0123456789-and-long-enough", "INTERNAL_TOKEN", "internal-token-0123456789-and-long-enough"},
		"the bridge's token":            {"SERVICE_SESSION_SECRET", "bridge-token-0123456789-and-long-enough!", "BRIDGE_TOKEN", "bridge-token-0123456789-and-long-enough!"},
		"with no login":                 {"SERVICE_SESSION_SECRET", serviceSecret, "AUTH_DISABLED", "true"},
		"a session longer than allowed": {"SERVICE_SESSION_SECRET", serviceSecret, "SERVICE_SESSION_TTL", "31m"},
		"a session of seconds":          {"SERVICE_SESSION_SECRET", serviceSecret, "SERVICE_SESSION_TTL", "59s"},
		"a lifetime that is not one":    {"SERVICE_SESSION_SECRET", serviceSecret, "SERVICE_SESSION_TTL", "soon"},
	} {
		_, err := Load(with(extra...))
		if err == nil {
			t.Errorf("%s: started", name)
			continue
		}
		// What is wrong is said by the variable's name. The value goes
		// to the log with the error, so it is never in it.
		for i := 1; i < len(extra); i += 2 {
			if len(extra[i]) >= 16 && strings.Contains(err.Error(), extra[i]) {
				t.Errorf("%s: the error repeats a secret: %v", name, err)
			}
		}
	}
}
