// demo-auth is intentionally limited to the local demonstration manifests.
// It is not used by the production chart.
package main

import (
	"crypto/subtle"
	"encoding/json"
	"errors"
	"log"
	"net/http"
	"os"
	"time"

	"github.com/spf13/cobra"
	"github.com/spf13/viper"
)

// main executes the demo authentication server command.
func main() {
	if err := newRootCommand().Execute(); err != nil {
		log.Print(err)
		os.Exit(1)
	}
}

// newRootCommand constructs the Cobra command and Viper environment mapping.
func newRootCommand() *cobra.Command {
	v := viper.New()
	v.AutomaticEnv()
	command := &cobra.Command{
		Use:           "jko-demo-auth",
		Short:         "Serve the local demo JupyterHub identity endpoint",
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE: func(_ *cobra.Command, _ []string) error {
			return run(v.GetString("DEMO_LISTEN_ADDRESS"), v.GetString("DEMO_TOKEN"), v.GetString("DEMO_USER"))
		},
	}
	v.SetDefault("DEMO_LISTEN_ADDRESS", ":8080")
	return command
}

// run validates demo credentials and serves the local identity endpoint.
func run(address, token, user string) error {
	if token == "" || user == "" {
		return errors.New("DEMO_TOKEN and DEMO_USER are required")
	}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	})
	mux.HandleFunc("GET /hub/api/user", userHandler(token, user))
	server := &http.Server{Addr: address, Handler: mux, ReadHeaderTimeout: 5 * time.Second}
	return server.ListenAndServe()
}

// userHandler returns a handler that validates the demo bearer token in constant time.
func userHandler(token, user string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		expected := "Bearer " + token
		provided := r.Header.Get("Authorization")
		if len(expected) != len(provided) || subtle.ConstantTimeCompare([]byte(expected), []byte(provided)) != 1 {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]string{"name": user})
	}
}
