package api

import (
	"fmt"
	"net/http"
)

// metrics exposes bounded, credential-free process metrics in Prometheus text
// format. It intentionally contains no user, workspace, path, or prompt labels.
func (s *Service) metrics(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "text/plain; version=0.0.4; charset=utf-8")
	ready := 0
	if s.ready.Load() {
		ready = 1
	}
	_, _ = fmt.Fprintf(w, `# HELP jko_mediator_ready Whether initial reconciliation completed.
# TYPE jko_mediator_ready gauge
jko_mediator_ready %d
# HELP jko_mediator_http_requests_total Total HTTP requests.
# TYPE jko_mediator_http_requests_total counter
jko_mediator_http_requests_total %d
# HELP jko_mediator_http_requests_in_flight Current HTTP requests.
# TYPE jko_mediator_http_requests_in_flight gauge
jko_mediator_http_requests_in_flight %d
# HELP jko_mediator_http_request_duration_seconds_total Aggregate HTTP request duration.
# TYPE jko_mediator_http_request_duration_seconds_total counter
jko_mediator_http_request_duration_seconds_total %.6f
# HELP jko_mediator_http_responses_total HTTP responses grouped by bounded status class.
# TYPE jko_mediator_http_responses_total counter
jko_mediator_http_responses_total{class="2xx"} %d
jko_mediator_http_responses_total{class="3xx"} %d
jko_mediator_http_responses_total{class="4xx"} %d
jko_mediator_http_responses_total{class="5xx"} %d
`, ready, s.requestCount.Load(), s.requestInFlight.Load(), float64(s.requestNanos.Load())/1e9, s.response2xx.Load(), s.response3xx.Load(), s.response4xx.Load(), s.response5xx.Load())
}
