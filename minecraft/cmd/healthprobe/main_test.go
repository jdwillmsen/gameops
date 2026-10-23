package main

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestAddr(t *testing.T) {
	for in, want := range map[string]string{
		"":              "127.0.0.1:8080",
		":9090":         "127.0.0.1:9090",
		"0.0.0.0:8081":  "127.0.0.1:8081",
		"10.1.2.3:8082": "10.1.2.3:8082",
		"[::]:8083":     "127.0.0.1:8083",
	} {
		if got := addr(in, ":8080"); got != want {
			t.Errorf("addr(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestProbe(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/healthz" {
			return
		}
		http.Error(w, "no", http.StatusServiceUnavailable)
	}))
	defer srv.Close()
	hp := strings.TrimPrefix(srv.URL, "http://")
	if err := probe(hp, "/healthz"); err != nil {
		t.Errorf("healthy: %v", err)
	}
	if err := probe(hp, "/bad"); err == nil {
		t.Error("503 should fail")
	}
}
