package auth

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"
)

func TestJupyterHubCachesOnlySuccessfulIntrospection(t *testing.T) {
	var calls atomic.Int32
	hub := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		if r.Header.Get("Authorization") != "Bearer valid-token-that-is-long" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		_, _ = w.Write([]byte(`{"name":"alice"}`))
	}))
	defer hub.Close()
	authenticator := NewJupyterHub(hub.URL, time.Second)
	for range 2 {
		if user, err := authenticator.Authenticate(context.Background(), "Bearer valid-token-that-is-long"); err != nil || user != "alice" {
			t.Fatalf("user=%q err=%v", user, err)
		}
	}
	if calls.Load() != 1 {
		t.Fatalf("positive introspection calls=%d, want 1", calls.Load())
	}
	for range 2 {
		if _, err := authenticator.Authenticate(context.Background(), "Bearer invalid-token-that-is-long"); err == nil {
			t.Fatal("invalid token was accepted")
		}
	}
	if calls.Load() != 3 {
		t.Fatalf("negative introspection was cached: calls=%d", calls.Load())
	}
}
