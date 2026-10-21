// demo-auth is intentionally limited to the local demonstration manifests.
// It is not used by the production chart.
package main

import (
	"crypto/subtle"
	"encoding/json"
	"log"
	"net/http"
	"os"
	"time"
)

func main() {
	token := os.Getenv("DEMO_TOKEN")
	user := os.Getenv("DEMO_USER")
	if token == "" || user == "" {
		log.Fatal("DEMO_TOKEN and DEMO_USER are required")
	}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	})
	mux.HandleFunc("GET /hub/api/user", func(w http.ResponseWriter, r *http.Request) {
		expected := "Bearer " + token
		provided := r.Header.Get("Authorization")
		if len(expected) != len(provided) || subtle.ConstantTimeCompare([]byte(expected), []byte(provided)) != 1 {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]string{"name": user})
	})
	server := &http.Server{Addr: ":8080", Handler: mux, ReadHeaderTimeout: 5 * time.Second}
	log.Fatal(server.ListenAndServe())
}

