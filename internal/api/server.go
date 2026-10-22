package api

import (
	"context"
	"net/http"
	"time"
)

// Run reconciles Agent policy and active tasks until the context is cancelled.
func (s *Service) Run(ctx context.Context) {
	ticker := time.NewTicker(s.cfg.ReconcileInterval)
	defer ticker.Stop()
	s.reconcileAll(ctx)
	s.ready.Store(true)
	defer s.ready.Store(false)
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			s.reconcileAll(ctx)
		}
	}
}

// health returns the service liveness response.
func (s *Service) liveness(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

// readiness reports whether the initial reconciliation has completed.
func (s *Service) readiness(w http.ResponseWriter, _ *http.Request) {
	if !s.ready.Load() {
		problem(w, http.StatusServiceUnavailable, "not_ready", "Initial reconciliation has not completed")
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "ready"})
}
