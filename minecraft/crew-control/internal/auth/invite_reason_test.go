package auth

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func errorCode(t *testing.T, rec *httptest.ResponseRecorder) string {
	t.Helper()
	var b struct {
		Error string `json:"error"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &b); err != nil {
		t.Fatalf("body %q: %v", rec.Body, err)
	}
	return b.Error
}

func TestSpentInviteIsNamedAsUsed(t *testing.T) {
	e := newEnv(t)
	mustOK(t, e.client().enrol(newSoftKey(t), "owner", "owner-code-0001"), "first")
	rec := e.client().post("/auth/register/options", map[string]string{"operator": "owner", "invite": "owner-code-0001"})
	if rec.Code != http.StatusForbidden || errorCode(t, rec) != "invite_used" {
		t.Fatalf("spent code: %d %s", rec.Code, rec.Body)
	}
}

func TestLosingTheRaceNamesTheInviteUsed(t *testing.T) {
	e := newEnv(t)
	a, b := e.client(), e.client()
	oa := a.post("/auth/register/options", map[string]string{"operator": "owner", "invite": "owner-code-0001"})
	ob := b.post("/auth/register/options", map[string]string{"operator": "owner", "invite": "owner-code-0001"})
	mustOK(t, a.finish("/auth/register/finish", newSoftKey(t).create(answer{rpID: rpID, origin: origin, challenge: challengeOf(t, oa)})), "winner")
	rec := b.finish("/auth/register/finish", newSoftKey(t).create(answer{rpID: rpID, origin: origin, challenge: challengeOf(t, ob)}))
	if rec.Code != http.StatusForbidden || errorCode(t, rec) != "invite_used" {
		t.Fatalf("loser: %d %s", rec.Code, rec.Body)
	}
}

// A guesser must not learn that a code once existed: only the true, spent
// code is named used.
func TestWrongInvitesStayGeneric(t *testing.T) {
	e := newEnv(t)
	mustOK(t, e.client().enrol(newSoftKey(t), "owner", "owner-code-0001"), "first")
	for _, tc := range []struct{ op, code string }{
		{"owner", "owner-code-0002"},
		{"josh", "owner-code-0001"},
		{"nobody", "owner-code-0001"},
		{"owner", ""},
	} {
		rec := e.client().post("/auth/register/options", map[string]string{"operator": tc.op, "invite": tc.code})
		if rec.Code != http.StatusForbidden || errorCode(t, rec) != "invite_refused" {
			t.Errorf("%+v: %d %s", tc, rec.Code, rec.Body)
		}
	}
}
