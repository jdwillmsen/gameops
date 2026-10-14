package operators

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestBridge_AsksWithTheTokenAndReadsTheMap(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/permissions" || r.Header.Get("Authorization") != "Bearer tok" {
			http.Error(w, "no", http.StatusUnauthorized)
			return
		}
		_, _ = w.Write([]byte(`{"` + steve + `":"operator","` + alex + `":"member"}`))
	}))
	defer srv.Close()
	got, err := NewBridge(srv.URL+"/", "tok").Permissions(t.Context())
	if err != nil || got[steve] != "operator" || got[alex] != "member" {
		t.Fatalf("Permissions = %v, %v", got, err)
	}
}

// A deleted file is an outage to ride out on the last-known-good list, not
// a list with nobody on it.
func TestBridge_AMissingFileIsAnErrorNotAnEmptyList(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "permissions.json not found", http.StatusNotFound)
	}))
	defer srv.Close()
	got, err := NewBridge(srv.URL, "tok").Permissions(t.Context())
	if !errors.Is(err, ErrNoFile) || got != nil {
		t.Fatalf("Permissions = %v, %v; want ErrNoFile", got, err)
	}
}
