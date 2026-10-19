package task

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func newAPI(t *testing.T, token string) (*http.ServeMux, *harness) {
	t.Helper()
	h := newHarness(t, time.Minute, 5)
	mux := http.NewServeMux()
	Routes(mux, h.r, token)
	return mux, h
}

func do(mux http.Handler, method, path, auth, body string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	if auth != "" {
		req.Header.Set("Authorization", auth)
	}
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	return rec
}

func TestTasksNeedTheBearerToken(t *testing.T) {
	mux, h := newAPI(t, "s3cret")
	for _, auth := range []string{"", "Bearer wrong", "Bearer", "s3cret", "Basic s3cret", "Bearer s3cret "} {
		for _, rt := range [][2]string{{"POST", "/v1/tasks"}, {"GET", "/v1/tasks"}, {"POST", "/v1/tasks/abc/cancel"}} {
			rec := do(mux, rt[0], rt[1], auth, goodBody)
			if rec.Code != http.StatusUnauthorized {
				t.Errorf("%s %s auth %q = %d", rt[0], rt[1], auth, rec.Code)
			}
			if strings.Contains(rec.Body.String(), "s3cret") {
				t.Error("response echoes the token")
			}
		}
	}
	if len(h.r.List()) != 0 {
		t.Error("an unauthorised request queued a task")
	}

	rec := do(mux, "POST", "/v1/tasks", "Bearer s3cret", goodBody)
	if rec.Code != http.StatusAccepted {
		t.Fatalf("submit = %d %s", rec.Code, rec.Body)
	}
	var created Task
	if err := json.Unmarshal(rec.Body.Bytes(), &created); err != nil || created.State != "queued" {
		t.Fatalf("body = %s (%v)", rec.Body, err)
	}
	if strings.Contains(rec.Body.String(), "chest") {
		t.Error("task body echoes the request")
	}

	rec = do(mux, "GET", "/v1/tasks", "Bearer s3cret", "")
	var list []Task
	if rec.Code != 200 || json.Unmarshal(rec.Body.Bytes(), &list) != nil || len(list) != 1 {
		t.Fatalf("list = %d %s", rec.Code, rec.Body)
	}
	if rec = do(mux, "POST", "/v1/tasks/"+created.ID+"/cancel", "Bearer s3cret", ""); rec.Code != 200 {
		t.Errorf("cancel = %d", rec.Code)
	}
	if rec = do(mux, "POST", "/v1/tasks/ffffffffffffffff/cancel", "Bearer s3cret", ""); rec.Code != 404 {
		t.Errorf("unknown = %d", rec.Code)
	}
	if rec = do(mux, "POST", "/v1/tasks/"+created.ID+"/cancel", "Bearer s3cret", ""); rec.Code != 409 {
		t.Errorf("already cancelled = %d", rec.Code)
	}
}

func TestSubmitRejectsBadAndOversizedBodies(t *testing.T) {
	mux, _ := newAPI(t, "tok")
	if rec := do(mux, "POST", "/v1/tasks", "Bearer tok", `{"template":"x"}`); rec.Code != 400 {
		t.Errorf("bad = %d", rec.Code)
	}
	huge := goodBody[:len(goodBody)-1] + `,"pad":"` + strings.Repeat("a", MaxBodyBytes) + `"}`
	if rec := do(mux, "POST", "/v1/tasks", "Bearer tok", huge); rec.Code != http.StatusRequestEntityTooLarge {
		t.Errorf("huge = %d", rec.Code)
	}
	for i := 0; i < MaxQueued; i++ {
		do(mux, "POST", "/v1/tasks", "Bearer tok", goodBody)
	}
	if rec := do(mux, "POST", "/v1/tasks", "Bearer tok", goodBody); rec.Code != http.StatusTooManyRequests {
		t.Errorf("full = %d", rec.Code)
	}
}

func TestTasksAreOffWithoutAToken(t *testing.T) {
	mux, _ := newAPI(t, "")
	for _, rt := range [][2]string{{"POST", "/v1/tasks"}, {"GET", "/v1/tasks"}, {"POST", "/v1/tasks/abc/cancel"}} {
		if rec := do(mux, rt[0], rt[1], "Bearer ", goodBody); rec.Code != http.StatusNotFound {
			t.Errorf("%s %s = %d, want 404", rt[0], rt[1], rec.Code)
		}
	}
}
